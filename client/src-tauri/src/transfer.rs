use std::fs::{self, File, OpenOptions};
use std::io::Read;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use tauri::Emitter;
use tauri_plugin_dialog::DialogExt;

use crate::{configs, jars, ProgressEvent, ProgressGate};

#[tauri::command]
pub(crate) async fn download_backup(
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
pub(crate) async fn upload_world(
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
