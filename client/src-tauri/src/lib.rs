mod backup;
mod configs;
mod jars;
mod update;

use std::cell::Cell;
use std::fs::{self, File, OpenOptions};
use std::io::{Read, Write};
use std::path::{Component, Path, PathBuf};
use std::time::{Duration, Instant};

use tauri::Emitter;

use serde::{Deserialize, Serialize};
use tauri_plugin_dialog::DialogExt;

pub const SERVER_URL: &str = env!("MODUPDATER_SERVER_URL");

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Config {
    #[serde(default)]
    pub mods_dir: String,
}

#[derive(Debug, Serialize, Deserialize)]
struct SessionFile {
    #[serde(default)]
    token: String,
}

pub(crate) fn validate_server_url(raw: &str) -> Result<(), String> {
    if raw.is_empty() {
        return Ok(());
    }
    let url = reqwest::Url::parse(raw).map_err(|_| "invalid_config".to_string())?;
    if url.scheme() != "http" && url.scheme() != "https" {
        return Err("invalid_config".into());
    }
    if url.host_str().is_none() {
        return Err("invalid_config".into());
    }
    if !url.username().is_empty() || url.password().is_some() {
        return Err("invalid_config".into());
    }
    let path = url.path();
    if !path.is_empty() && path != "/" {
        return Err("invalid_config".into());
    }
    if url.query().is_some() || url.fragment().is_some() {
        return Err("invalid_config".into());
    }
    Ok(())
}

pub(crate) fn normalize_exe_path(exe: &Path) -> PathBuf {
    if exe.canonicalize().is_ok() {
        return exe.to_path_buf();
    }
    let text = exe.to_string_lossy();
    if let Some(stripped) = text.strip_suffix(" (deleted)") {
        let retry = PathBuf::from(stripped);
        if retry.canonicalize().is_ok() {
            return retry;
        }
    }
    exe.to_path_buf()
}

pub(crate) fn config_dir_from_exe(exe: &Path) -> Result<PathBuf, String> {
    let canon = normalize_exe_path(exe)
        .canonicalize()
        .map_err(|_| "invalid_config".to_string())?;
    Ok(config_dir_from_canonical(&canon))
}

fn config_dir_from_canonical(canon: &Path) -> PathBuf {
    let comps: Vec<Component> = canon.components().collect();
    for index in 0..comps.len() {
        let name = component_name(&comps[index]);
        if name.ends_with(".app") {
            let next = comps.get(index + 1).map(component_name);
            let after = comps.get(index + 2).map(component_name);
            if next.as_deref() == Some("Contents") && after.as_deref() == Some("MacOS") {
                let app = path_from(&comps, index + 1);
                if let Some(parent) = app.parent() {
                    if !parent.as_os_str().is_empty() {
                        return parent.to_path_buf();
                    }
                }
            }
        }
    }
    canon
        .parent()
        .map(Path::to_path_buf)
        .filter(|path| !path.as_os_str().is_empty())
        .unwrap_or_else(|| PathBuf::from("/"))
}

fn component_name(component: &Component) -> String {
    match component {
        Component::Normal(name) => name.to_string_lossy().into_owned(),
        _ => String::new(),
    }
}

fn path_from(comps: &[Component], len: usize) -> PathBuf {
    let mut path = PathBuf::new();
    for component in comps.iter().take(len) {
        match component {
            Component::Prefix(prefix) => path.push(prefix.as_os_str()),
            Component::RootDir => path.push(component.as_os_str()),
            Component::Normal(name) => path.push(name),
            Component::CurDir => {}
            Component::ParentDir => path.push(".."),
        }
    }
    path
}

fn config_dir() -> Result<PathBuf, String> {
    let exe = std::env::current_exe().map_err(|_| "invalid_config".to_string())?;
    config_dir_from_exe(&normalize_exe_path(&exe))
}

pub(crate) fn load_config_from(dir: &Path) -> Result<Config, String> {
    let path = dir.join("config.json");
    if is_symlink(&path) {
        return Err("invalid_config".into());
    }
    if !path.exists() {
        return Ok(Config::default());
    }
    let bytes = fs::read(&path).map_err(|_| "invalid_config".to_string())?;
    let cfg: Config = serde_json::from_slice(&bytes).map_err(|_| "invalid_config".to_string())?;
    Ok(cfg)
}

pub(crate) fn save_config_to(dir: &Path, mods_dir: &str) -> Result<(), String> {
    validate_mods_dir(mods_dir)?;
    if !dir.is_dir() {
        return Err("invalid_config".into());
    }
    let cfg = Config {
        mods_dir: mods_dir.to_string(),
    };
    let bytes = serde_json::to_vec_pretty(&cfg).map_err(|_| "internal".to_string())?;
    write_private(&dir.join("config.json"), &bytes)
}

pub(crate) fn load_session_from(dir: &Path) -> Result<String, String> {
    let path = dir.join("session.json");
    if is_symlink(&path) {
        return Err("invalid_config".into());
    }
    if !path.exists() {
        return Ok(String::new());
    }
    let bytes = fs::read(&path).map_err(|_| "invalid_config".to_string())?;
    let parsed: SessionFile =
        serde_json::from_slice(&bytes).map_err(|_| "invalid_config".to_string())?;
    Ok(parsed.token)
}

pub(crate) fn save_session_to(dir: &Path, token: &str) -> Result<(), String> {
    let path = dir.join("session.json");
    if is_symlink(&path) {
        return Err("invalid_config".into());
    }
    if token.is_empty() {
        return match fs::remove_file(&path) {
            Ok(()) => Ok(()),
            Err(err) if err.kind() == std::io::ErrorKind::NotFound => Ok(()),
            Err(_) => Err("invalid_config".into()),
        };
    }
    let bytes = serde_json::to_vec_pretty(&SessionFile {
        token: token.to_string(),
    })
    .map_err(|_| "internal".to_string())?;
    write_private(&path, &bytes)
}

fn validate_mods_dir(raw: &str) -> Result<(), String> {
    if raw.is_empty() {
        return Ok(());
    }
    let meta = fs::metadata(raw).map_err(|_| "invalid_config".to_string())?;
    if !meta.is_dir() {
        return Err("invalid_config".into());
    }
    Ok(())
}

fn is_symlink(path: &Path) -> bool {
    fs::symlink_metadata(path)
        .map(|meta| meta.file_type().is_symlink())
        .unwrap_or(false)
}

pub(crate) fn write_private(path: &Path, bytes: &[u8]) -> Result<(), String> {
    if is_symlink(path) {
        return Err("invalid_config".into());
    }
    let mut opts = OpenOptions::new();
    opts.write(true).create(true).truncate(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        opts.mode(0o600);
    }
    let mut file = opts.open(path).map_err(|_| "invalid_config".to_string())?;
    file.write_all(bytes)
        .map_err(|_| "invalid_config".to_string())?;
    file.sync_all().map_err(|_| "invalid_config".to_string())?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(path, fs::Permissions::from_mode(0o600))
            .map_err(|_| "invalid_config".to_string())?;
    }
    Ok(())
}

fn mods_dir_from_config(cfg: &Config) -> Result<PathBuf, String> {
    if cfg.mods_dir.is_empty() {
        return Err("invalid_config".into());
    }
    let dir = Path::new(&cfg.mods_dir)
        .canonicalize()
        .map_err(|_| "invalid_config".to_string())?;
    if !dir.is_dir() {
        return Err("invalid_config".into());
    }
    Ok(dir)
}

#[tauri::command]
fn load_config() -> Result<Config, String> {
    load_config_from(&config_dir()?)
}

#[tauri::command]
fn save_config(mods_dir: String) -> Result<(), String> {
    save_config_to(&config_dir()?, &mods_dir)
}

#[tauri::command]
fn load_session() -> Result<String, String> {
    load_session_from(&config_dir()?)
}

#[tauri::command]
fn save_session(token: String) -> Result<(), String> {
    save_session_to(&config_dir()?, &token)
}

#[tauri::command]
fn scan_mods() -> Result<Vec<jars::LocalJar>, String> {
    let cfg = load_config()?;
    jars::scan_dir(&mods_dir_from_config(&cfg)?)
}

#[derive(Clone, serde::Serialize)]
struct ProgressEvent {
    loaded: u64,
    total: u64,
}

#[tauri::command]
fn install_mod(
    app: tauri::AppHandle,
    mod_id: String,
    filename: String,
    sha256: String,
    backup_filename: Option<String>,
) -> Result<(), String> {
    let cfg = load_config()?;
    jars::install_jar(
        &mods_dir_from_config(&cfg)?,
        SERVER_URL,
        &mod_id,
        &filename,
        &sha256,
        backup_filename.as_deref(),
        &|loaded, total| {
            let _ = app.emit("install-progress", ProgressEvent { loaded, total });
        },
    )
}

#[tauri::command]
fn app_channel() -> String {
    match std::env::var("MODUPDATER_CHANNEL") {
        Ok(value) if value == "dev" => "dev".into(),
        _ => String::new(),
    }
}

#[tauri::command]
fn current_os() -> String {
    update::current_os_name()
}

#[tauri::command]
fn self_update(app: tauri::AppHandle, version: String, sha256: String) -> Result<(), String> {
    let exe = std::env::current_exe().map_err(|_| "internal".to_string())?;
    update::self_update(&exe, SERVER_URL, &version, &sha256, &|loaded, total| {
        let _ = app.emit("update-progress", ProgressEvent { loaded, total });
    })?;
    update::spawn_detached(&exe, None)?;
    std::process::exit(0);
}

#[tauri::command]
fn launch_dev_release(
    app: tauri::AppHandle,
    version: String,
    sha256: String,
    token: String,
) -> Result<(), String> {
    let exe = std::env::current_exe().map_err(|_| "internal".to_string())?;
    let cfg = load_config()?;
    let dest = update::launch_dev(
        &exe,
        &config_dir()?,
        &cfg.mods_dir,
        SERVER_URL,
        &version,
        &sha256,
        &token,
        &|loaded, total| {
            let _ = app.emit("update-progress", ProgressEvent { loaded, total });
        },
    )?;
    update::spawn_detached(&dest, Some("dev"))
}

#[tauri::command]
async fn download_backup(
    app: tauri::AppHandle,
    server_url: String,
    token: String,
    id: String,
    filename: String,
) -> Result<bool, String> {
    if !valid_backup_id(&id) {
        return Err("Бекап не найден.".into());
    }
    if !safe_zip_name(&filename) {
        return Err("Некорректное имя файла.".into());
    }
    let picked = app
        .dialog()
        .file()
        .set_title("Скачать бекап")
        .set_file_name(&filename)
        .blocking_save_file();
    let Some(picked) = picked else {
        return Ok(false);
    };
    let path = picked
        .into_path()
        .map_err(|_| "Некорректный путь.".to_string())?;
    tauri::async_runtime::spawn_blocking(move || save_backup(&app, &path, &server_url, &token, &id))
        .await
        .map_err(|_| "Не удалось скачать бекап.".to_string())?
}

fn valid_backup_id(id: &str) -> bool {
    id.len() == 32 && id.bytes().all(|byte| byte.is_ascii_hexdigit())
}

fn safe_zip_name(name: &str) -> bool {
    !name.is_empty()
        && name.len() <= 160
        && name.ends_with(".tar.zst")
        && name.bytes().all(|byte| {
            byte.is_ascii_alphanumeric() || byte == b'-' || byte == b'_' || byte == b'.'
        })
}

#[tauri::command]
async fn save_log(app: tauri::AppHandle, filename: String, text: String) -> Result<bool, String> {
    if !safe_log_name(&filename) {
        return Err("Некорректное имя файла.".into());
    }
    let picked = app
        .dialog()
        .file()
        .set_title("Скачать лог")
        .set_file_name(&filename)
        .blocking_save_file();
    let Some(picked) = picked else {
        return Ok(false);
    };
    let path = picked
        .into_path()
        .map_err(|_| "Некорректный путь.".to_string())?;
    tauri::async_runtime::spawn_blocking(move || fs::write(&path, text))
        .await
        .map_err(|_| "Не удалось сохранить лог.".to_string())?
        .map_err(|_| "Не удалось сохранить лог.".to_string())?;
    Ok(true)
}

fn safe_log_name(name: &str) -> bool {
    !name.is_empty()
        && name.len() <= 80
        && name.ends_with(".txt")
        && !name.contains("..")
        && name.bytes().all(|byte| {
            byte.is_ascii_alphanumeric() || byte == b'-' || byte == b'_' || byte == b'.'
        })
}

fn save_backup(
    app: &tauri::AppHandle,
    path: &Path,
    server_url: &str,
    token: &str,
    id: &str,
) -> Result<bool, String> {
    let url = jars::api_url(server_url, &format!("/api/admin/backups/{id}/file"))?;
    OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .open(path)
        .map_err(|_| "Не удалось скачать бекап.".to_string())?;
    let gate = ProgressGate::new();
    let saved = path.to_path_buf();
    let result = jars::download_within(
        &url,
        path,
        server_url,
        Some(token),
        &|loaded, total| {
            let now = Instant::now();
            let done = total > 0 && loaded >= total;
            if loaded != 0 && !done && now < gate.next.get() {
                return;
            }
            gate.next.set(now + Duration::from_millis(100));
            let _ = app.emit("backup-download", ProgressEvent { loaded, total });
        },
        None,
    );
    if let Err(err) = result {
        let _ = fs::remove_file(&saved);
        return Err(err);
    }
    Ok(true)
}

#[tauri::command]
async fn upload_world(
    app: tauri::AppHandle,
    server_url: String,
    token: String,
) -> Result<bool, String> {
    let picked = app
        .dialog()
        .file()
        .set_title("Папка мира")
        .blocking_pick_folder();
    let Some(picked) = picked else {
        return Ok(false);
    };
    let path = picked
        .into_path()
        .map_err(|_| "Некорректный путь.".to_string())?;
    tauri::async_runtime::spawn_blocking(move || upload_world_dir(&app, &path, &server_url, &token))
        .await
        .map_err(|_| "Не удалось загрузить мир.".to_string())?
}

fn upload_world_dir(
    app: &tauri::AppHandle,
    dir: &Path,
    server_url: &str,
    token: &str,
) -> Result<bool, String> {
    configs::require_level_dat(dir)?;
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|item| item.as_nanos())
        .unwrap_or(0);
    let zip_path =
        std::env::temp_dir().join(format!("mu-world-{}-{nanos}.zip", std::process::id()));
    let _cleanup = RemoveFile(zip_path.clone());
    configs::pack_directory(dir, &zip_path).map_err(|err| match err.as_str() {
        "invalid_path" => "Некорректный путь.".to_string(),
        _ => "Не удалось упаковать мир.".to_string(),
    })?;
    let file = File::open(&zip_path).map_err(|_| "Не удалось упаковать мир.".to_string())?;
    let total = file
        .metadata()
        .map_err(|_| "Не удалось упаковать мир.".to_string())?
        .len();
    let _ = app.emit("world-upload", ProgressEvent { loaded: 0, total });
    let reader = CountingFile {
        file,
        done: 0,
        total,
        next: Instant::now(),
        app: app.clone(),
    };
    if token.is_empty() || token.bytes().any(|byte| byte <= 0x20 || byte >= 0x7f) {
        return Err("unauthorized".into());
    }
    let url = jars::api_url(server_url, "/api/admin/backups/world")?;
    let client = reqwest::blocking::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .connect_timeout(Duration::from_secs(10))
        .no_proxy()
        .build()
        .map_err(|_| "Не удалось загрузить мир.".to_string())?;
    let response = client
        .post(url)
        .bearer_auth(token)
        .header("content-type", "application/zip")
        .body(reqwest::blocking::Body::sized(reader, total))
        .send()
        .map_err(|_| "Не удалось загрузить мир.".to_string())?;
    let status = response.status();
    if status.is_redirection() {
        return Err("Не удалось загрузить мир.".into());
    }
    if !status.is_success() {
        let text = response.text().unwrap_or_default();
        return Err(upload_error(&text));
    }
    Ok(true)
}

fn upload_error(body: &str) -> String {
    let value: serde_json::Value = serde_json::from_str(body).unwrap_or(serde_json::Value::Null);
    if let Some(err) = value.get("error").and_then(|item| item.as_str()) {
        if !err.is_empty() {
            return err.to_string();
        }
    }
    "Не удалось загрузить мир.".into()
}

struct RemoveFile(PathBuf);

impl Drop for RemoveFile {
    fn drop(&mut self) {
        let _ = fs::remove_file(&self.0);
    }
}

struct CountingFile {
    file: File,
    done: u64,
    total: u64,
    next: Instant,
    app: tauri::AppHandle,
}

impl Read for CountingFile {
    fn read(&mut self, buf: &mut [u8]) -> std::io::Result<usize> {
        let n = self.file.read(buf)?;
        if n == 0 {
            return Ok(0);
        }
        self.done += n as u64;
        let now = Instant::now();
        let finished = self.total == 0 || self.done >= self.total;
        if finished || now >= self.next {
            self.next = now + Duration::from_millis(100);
            let _ = self.app.emit(
                "world-upload",
                ProgressEvent {
                    loaded: self.done,
                    total: self.total,
                },
            );
        }
        Ok(n)
    }
}

#[tauri::command]
async fn pack_dir_zip(app: tauri::AppHandle) -> Result<Option<configs::PackedZip>, String> {
    let picked = app
        .dialog()
        .file()
        .set_title("Папка конфига")
        .blocking_pick_folder();
    let Some(picked) = picked else {
        return Ok(None);
    };
    let path = picked.into_path().map_err(|_| "invalid_path".to_string())?;
    Ok(Some(configs::pack_dir_zip(&path)?))
}

#[tauri::command]
fn apply_mod_config(
    app: tauri::AppHandle,
    mod_id: String,
    config_id: String,
    dest: String,
    kind: String,
    sha256: String,
) -> Result<(), String> {
    let cfg = load_config()?;
    configs::apply_mod_config(
        &mods_dir_from_config(&cfg)?,
        SERVER_URL,
        &mod_id,
        &config_id,
        &dest,
        &kind,
        &sha256,
        &|loaded, total| {
            let _ = app.emit("config-progress", ProgressEvent { loaded, total });
        },
    )
}

#[tauri::command]
fn load_config_registry() -> Result<std::collections::BTreeMap<String, String>, String> {
    let cfg = load_config()?;
    let mods = mods_dir_from_config(&cfg)?;
    configs::load_registry(&backup::version_root(&mods)?)
}

struct ProgressGate {
    next: Cell<Instant>,
}

impl ProgressGate {
    fn new() -> Self {
        Self {
            next: Cell::new(Instant::now()),
        }
    }

    fn tick(&self, app: &tauri::AppHandle, loaded: u64, total: u64) {
        let now = Instant::now();
        let done = loaded >= total;
        if loaded != 0 && !done && now < self.next.get() {
            return;
        }
        self.next.set(now + Duration::from_millis(100));
        let _ = app.emit("backup-progress", ProgressEvent { loaded, total });
    }
}

#[tauri::command]
async fn ensure_instance_backup(app: tauri::AppHandle) -> Result<backup::BackupOutcome, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let cfg = load_config()?;
        let mods = mods_dir_from_config(&cfg)?;
        let gate = ProgressGate::new();
        backup::ensure_instance_backup(&backup::version_root(&mods)?, &|loaded, total| {
            gate.tick(&app, loaded, total);
        })
    })
    .await
    .map_err(|_| "internal".to_string())?
}

#[tauri::command]
async fn create_instance_backup(app: tauri::AppHandle) -> Result<backup::BackupOutcome, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let cfg = load_config()?;
        let mods = mods_dir_from_config(&cfg)?;
        let gate = ProgressGate::new();
        backup::create_instance_backup(&backup::version_root(&mods)?, &|loaded, total| {
            gate.tick(&app, loaded, total);
        })
    })
    .await
    .map_err(|_| "internal".to_string())?
}

#[tauri::command]
fn inspect_launcher() -> Result<backup::LauncherInfo, String> {
    let cfg = load_config()?;
    backup::inspect_launcher(&mods_dir_from_config(&cfg)?)
}

#[tauri::command]
async fn duplicate_version(
    app: tauri::AppHandle,
    src: String,
    new_name: String,
) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        let cfg = load_config()?;
        let mods = mods_dir_from_config(&cfg)?;
        let gate = ProgressGate::new();
        backup::duplicate_version(&mods, &src, &new_name, &|loaded, total| {
            gate.tick(&app, loaded, total);
        })
    })
    .await
    .map_err(|_| "internal".to_string())?
}

#[tauri::command]
fn backup_mod(filename: String) -> Result<(), String> {
    let cfg = load_config()?;
    jars::backup_jar(&mods_dir_from_config(&cfg)?, &filename)
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    if let Ok(exe) = std::env::current_exe() {
        update::cleanup_startup(&exe);
    }
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .invoke_handler(tauri::generate_handler![
            load_config,
            save_config,
            load_session,
            save_session,
            scan_mods,
            install_mod,
            backup_mod,
            app_channel,
            current_os,
            self_update,
            launch_dev_release,
            pack_dir_zip,
            upload_world,
            download_backup,
            save_log,
            apply_mod_config,
            load_config_registry,
            ensure_instance_backup,
            create_instance_backup,
            inspect_launcher,
            duplicate_version
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::time::{SystemTime, UNIX_EPOCH};

    struct Tmp {
        path: PathBuf,
    }

    impl Tmp {
        fn new(label: &str) -> Self {
            static N: AtomicUsize = AtomicUsize::new(0);
            let n = N.fetch_add(1, Ordering::Relaxed);
            let nanos = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos();
            let path = std::env::temp_dir().join(format!("modupdater-cfg-{label}-{n}-{nanos}"));
            fs::create_dir_all(&path).unwrap();
            Self { path }
        }
    }

    impl Drop for Tmp {
        fn drop(&mut self) {
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                if let Ok(meta) = fs::metadata(&self.path) {
                    let mut perms = meta.permissions();
                    perms.set_mode(0o755);
                    let _ = fs::set_permissions(&self.path, perms);
                }
            }
            let _ = fs::remove_dir_all(&self.path);
        }
    }

    fn mode(path: &Path) -> u32 {
        use std::os::unix::fs::PermissionsExt;
        fs::metadata(path).unwrap().permissions().mode() & 0o777
    }

    #[test]
    fn replaced_executable_still_resolves_config_dir() {
        let tmp = Tmp::new("replaced");
        let exe = tmp.path.join("modupdater");
        fs::write(&exe, b"bin").unwrap();
        let deleted = PathBuf::from(format!("{} (deleted)", exe.display()));
        let dir = config_dir_from_exe(&deleted).unwrap();
        assert_eq!(dir, tmp.path.canonicalize().unwrap());
    }

    #[test]
    fn config_sits_beside_a_normal_executable() {
        let tmp = Tmp::new("exe");
        let exe = tmp.path.join("modupdater");
        fs::write(&exe, b"bin").unwrap();
        let dir = config_dir_from_exe(&exe).unwrap();
        assert_eq!(dir, tmp.path.canonicalize().unwrap());
        let mods = tmp.path.join("mods");
        fs::create_dir(&mods).unwrap();
        save_config_to(&dir, mods.to_str().unwrap()).unwrap();
        let cfg_path = dir.join("config.json");
        assert!(cfg_path.is_file());
        assert_eq!(mode(&cfg_path), 0o600);
        let raw: serde_json::Value = serde_json::from_slice(&fs::read(&cfg_path).unwrap()).unwrap();
        assert!(raw.get("server_url").is_none());
        assert_eq!(raw["mods_dir"], mods.to_str().unwrap());
        let loaded = load_config_from(&dir).unwrap();
        assert_eq!(loaded.mods_dir, mods.to_str().unwrap());
        fs::write(
            &cfg_path,
            br#"{"server_url":"http://127.0.0.1:9","mods_dir":"kept"}"#,
        )
        .unwrap();
        assert_eq!(load_config_from(&dir).unwrap().mods_dir, "kept");
        save_session_to(&dir, "abc").unwrap();
        let session = dir.join("session.json");
        assert_eq!(mode(&session), 0o600);
        assert_eq!(load_session_from(&dir).unwrap(), "abc");
        save_session_to(&dir, "").unwrap();
        assert!(!session.exists());
        save_session_to(&dir, "").unwrap();
        assert_eq!(load_session_from(&dir).unwrap(), "");
    }

    #[test]
    fn config_sits_beside_app_bundle() {
        let tmp = Tmp::new("app");
        let macos = tmp.path.join("Client.app").join("Contents").join("MacOS");
        fs::create_dir_all(&macos).unwrap();
        let exe = macos.join("modupdater");
        fs::write(&exe, b"bin").unwrap();
        let dir = config_dir_from_exe(&exe).unwrap();
        assert_eq!(dir, tmp.path.canonicalize().unwrap());
        save_config_to(&dir, "").unwrap();
        assert!(tmp.path.join("config.json").is_file());
        assert!(!macos.join("config.json").exists());
        assert!(!tmp.path.join("Client.app").join("config.json").exists());
    }

    #[test]
    fn app_suffix_without_macos_uses_executable_parent() {
        let tmp = Tmp::new("fakeapp");
        let folder = tmp.path.join("Foo.app");
        fs::create_dir_all(&folder).unwrap();
        let exe = folder.join("modupdater");
        fs::write(&exe, b"bin").unwrap();
        let dir = config_dir_from_exe(&exe).unwrap();
        assert_eq!(dir, folder.canonicalize().unwrap());
    }

    #[test]
    fn save_rejects_bad_server_urls_and_missing_mods_dir() {
        let tmp = Tmp::new("urls");
        let dir = tmp.path.canonicalize().unwrap();
        for bad in [
            "http://example.com/path",
            "http://user@example.com",
            "http://user:secret@example.com",
            "http://example.com?q=1",
            "http://example.com/#frag",
            "ftp://example.com",
            "file:///tmp",
            "http://",
        ] {
            assert!(validate_server_url(bad).is_err(), "{bad}");
        }
        assert!(validate_server_url("").is_ok());
        assert!(validate_server_url("http://127.0.0.1:8080").is_ok());
        assert!(validate_server_url("https://mods.example.com").is_ok());
        assert!(validate_server_url("http://example.com/").is_ok());
        assert!(validate_server_url(SERVER_URL).is_ok());
        assert!(!SERVER_URL.ends_with('/'));
        assert!(save_config_to(&dir, "/no/such/mods").is_err());
        let file = dir.join("not-a-dir");
        fs::write(&file, b"x").unwrap();
        assert!(save_config_to(&dir, file.to_str().unwrap()).is_err());
        assert!(load_config_from(&dir).unwrap().mods_dir.is_empty());
    }

    #[test]
    fn unwritable_config_dir_returns_error() {
        if is_root() {
            return;
        }
        let tmp = Tmp::new("ro");
        let exe = tmp.path.join("modupdater");
        fs::write(&exe, b"x").unwrap();
        let dir = config_dir_from_exe(&exe).unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mut perms = fs::metadata(&dir).unwrap().permissions();
            perms.set_mode(0o555);
            fs::set_permissions(&dir, perms).unwrap();
        }
        let err = save_config_to(&dir, "");
        assert!(err.is_err());
        assert!(!dir.join("config.json").exists());
    }

    fn is_root() -> bool {
        let text = fs::read_to_string("/proc/self/status").unwrap_or_default();
        for line in text.lines() {
            if let Some(rest) = line.strip_prefix("Uid:") {
                return rest.split_whitespace().nth(1) == Some("0");
            }
        }
        false
    }

    #[test]
    fn capabilities_do_not_grant_shell_or_filesystem() {
        let path = Path::new(env!("CARGO_MANIFEST_DIR")).join("capabilities/default.json");
        let text = fs::read_to_string(&path).unwrap();
        let value: serde_json::Value = serde_json::from_str(&text).unwrap();
        let mut strings = Vec::new();
        walk(&value, &mut strings);
        for item in &strings {
            let lower = item.to_ascii_lowercase();
            assert!(!lower.contains("shell"), "{item}");
            assert!(!lower.contains("fs:"), "{item}");
            assert!(lower != "fs" && lower != "filesystem", "{item}");
        }
        let permissions = value
            .get("permissions")
            .and_then(|item| item.as_array())
            .unwrap();
        assert!(permissions
            .iter()
            .any(|item| item.as_str() == Some("dialog:allow-open")));
        assert!(!text.contains("fs:allow"));
    }

    fn walk(value: &serde_json::Value, out: &mut Vec<String>) {
        match value {
            serde_json::Value::String(text) => out.push(text.clone()),
            serde_json::Value::Array(items) => {
                for item in items {
                    walk(item, out);
                }
            }
            serde_json::Value::Object(map) => {
                for item in map.values() {
                    walk(item, out);
                }
            }
            _ => {}
        }
    }

    #[test]
    fn log_download_names_stay_plain() {
        assert!(safe_log_name("log-hour-20261106-140509.txt"));
        assert!(safe_log_name("log-day-20260102-030405.txt"));
        assert!(safe_log_name("log-all-20261106-000000.txt"));
        assert!(!safe_log_name(""));
        assert!(!safe_log_name("../log.txt"));
        assert!(!safe_log_name("log/hour.txt"));
        assert!(!safe_log_name("лог.txt"));
        assert!(!safe_log_name("log.txt.exe"));
    }

    #[test]
    fn config_code_does_not_fall_back_to_os_app_data() {
        let mut src = String::new();
        for name in [
            "src/lib.rs",
            "src/jars.rs",
            "src/backup.rs",
            "src/configs.rs",
            "src/update.rs",
        ] {
            src.push_str(
                &fs::read_to_string(Path::new(env!("CARGO_MANIFEST_DIR")).join(name)).unwrap(),
            );
        }
        for needle in [
            ["Project", "Dirs"].concat(),
            ["app", "_data", "_dir"].concat(),
            ["dirs", "::"].concat(),
        ] {
            assert!(!src.contains(&needle), "{needle}");
        }
    }
}
