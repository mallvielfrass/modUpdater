use std::fs::{self, OpenOptions};
use std::io::Write;
use std::path::{Component, Path, PathBuf};

use serde::{Deserialize, Serialize};

use crate::Config;

#[derive(Debug, Serialize, Deserialize)]
struct SessionFile {
    #[serde(default)]
    token: String,
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

pub(crate) fn config_dir() -> Result<PathBuf, String> {
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
