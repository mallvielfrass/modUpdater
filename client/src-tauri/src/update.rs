use std::fs::{self, OpenOptions};
use std::path::{Path, PathBuf};

use crate::jars::{self, ct_eq};
use crate::save_config_to;

const LOCK_NAME: &str = ".mu-update.lock";

pub fn release_os() -> Result<&'static str, String> {
    if cfg!(target_os = "windows") {
        Ok("windows")
    } else if cfg!(target_os = "linux") {
        Ok("linux")
    } else {
        Err("unsupported_os".into())
    }
}

pub fn current_os_name() -> String {
    release_os().unwrap_or("other").to_string()
}

pub fn valid_client_version(raw: &str) -> bool {
    if raw.is_empty() || raw.len() > 64 {
        return false;
    }
    let (core, pre) = match raw.split_once('-') {
        Some((core, pre)) => (core, Some(pre)),
        None => (raw, None),
    };
    let parts: Vec<&str> = core.split('.').collect();
    if parts.len() != 3 || !parts.iter().copied().all(numeric_part) {
        return false;
    }
    let Some(pre) = pre else {
        return true;
    };
    if pre.is_empty() || pre.len() > 32 {
        return false;
    }
    pre.split('.').all(|part| {
        !part.is_empty()
            && part
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-')
    })
}

fn numeric_part(part: &str) -> bool {
    !part.is_empty()
        && part.len() <= 6
        && part.bytes().all(|byte| byte.is_ascii_digit())
        && (part.len() == 1 || !part.starts_with('0'))
}

pub fn old_exe_path(exe: &Path) -> PathBuf {
    let mut name = exe.file_name().unwrap_or_default().to_os_string();
    name.push(".old");
    exe.with_file_name(name)
}

pub fn cleanup_startup(exe: &Path) {
    let _ = fs::remove_file(old_exe_path(exe));
    if let Some(dir) = exe.parent() {
        let _ = fs::remove_file(dir.join(LOCK_NAME));
    }
}

pub fn replace_executable(current: &Path, staged: &Path) -> Result<(), String> {
    if !staged.is_file() || !current.is_file() {
        return Err("internal".into());
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(staged, fs::Permissions::from_mode(0o755))
            .map_err(|_| "internal".to_string())?;
    }
    let old = old_exe_path(current);
    if fs::symlink_metadata(&old).is_ok() {
        fs::remove_file(&old).map_err(|_| "internal".to_string())?;
    }
    fs::rename(current, &old).map_err(|_| "internal".to_string())?;
    if fs::rename(staged, current).is_err() {
        let _ = fs::rename(&old, current);
        return Err("internal".into());
    }
    Ok(())
}

pub fn self_update(
    exe: &Path,
    server_url: &str,
    version: &str,
    sha256: &str,
    progress: &dyn Fn(u64, u64),
) -> Result<PathBuf, String> {
    let os = release_os()?;
    if !valid_client_version(version) {
        return Err("invalid_version".into());
    }
    let expect = normalize_sha(sha256)?;
    let dir = exe
        .parent()
        .filter(|path| !path.as_os_str().is_empty())
        .ok_or_else(|| "internal".to_string())?
        .canonicalize()
        .map_err(|_| "internal".to_string())?;
    let lock_path = dir.join(LOCK_NAME);
    let _lock = match OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&lock_path)
    {
        Ok(_) => LockGuard {
            path: lock_path,
            keep: false,
        },
        Err(_) => return Err("busy".into()),
    };
    let url = jars::api_url(
        server_url,
        &format!("/api/client/releases/{version}/{os}/file"),
    )?;
    let temp = jars::create_temp(&dir)?;
    let mut staged = Staged {
        path: temp,
        keep: false,
    };
    let hash = jars::download(&url, &staged.path, server_url, None, progress)?;
    if !ct_eq(&hash, &expect) {
        return Err("hash_mismatch".into());
    }
    replace_executable(exe, &staged.path)?;
    staged.keep = true;
    Ok(exe.to_path_buf())
}

pub fn launch_dev(
    exe: &Path,
    config_dir: &Path,
    mods_dir: &str,
    server_url: &str,
    version: &str,
    sha256: &str,
    token: &str,
    progress: &dyn Fn(u64, u64),
) -> Result<PathBuf, String> {
    let os = release_os()?;
    if !valid_client_version(version) {
        return Err("invalid_version".into());
    }
    let expect = normalize_sha(sha256)?;
    if token.is_empty() {
        return Err("unauthorized".into());
    }
    let exe_real = fs::canonicalize(exe).unwrap_or_else(|_| exe.to_path_buf());
    let runs = config_dir.join("dev-runs").join(version);
    fs::create_dir_all(&runs).map_err(|_| "internal".to_string())?;
    let runs = runs.canonicalize().map_err(|_| "internal".to_string())?;
    let dev_root = config_dir
        .join("dev-runs")
        .canonicalize()
        .map_err(|_| "internal".to_string())?;
    if !runs.starts_with(&dev_root) {
        return Err("invalid_path".into());
    }
    let dest = runs.join(dev_binary_name(os));
    if dest == exe_real {
        return Err("internal".into());
    }
    let url = jars::api_url(
        server_url,
        &format!("/api/admin/releases/{version}/{os}/file"),
    )?;
    let temp = jars::create_temp(&runs)?;
    let mut staged = Staged {
        path: temp,
        keep: false,
    };
    let hash = jars::download(&url, &staged.path, server_url, Some(token), progress)?;
    if !ct_eq(&hash, &expect) {
        return Err("hash_mismatch".into());
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&staged.path, fs::Permissions::from_mode(0o755))
            .map_err(|_| "internal".to_string())?;
    }
    if fs::symlink_metadata(&dest).is_ok() {
        fs::remove_file(&dest).map_err(|_| "internal".to_string())?;
    }
    fs::rename(&staged.path, &dest).map_err(|_| "internal".to_string())?;
    staged.keep = true;
    save_config_to(&runs, mods_dir)?;
    Ok(dest)
}

pub fn spawn_detached(exe: &Path, channel: Option<&str>) -> Result<(), String> {
    let mut cmd = std::process::Command::new(exe);
    if let Some(value) = channel {
        cmd.env("MODUPDATER_CHANNEL", value);
    }
    cmd.spawn().map_err(|_| "internal".to_string())?;
    Ok(())
}

fn dev_binary_name(os: &str) -> &'static str {
    if os == "windows" {
        "modupdater.exe"
    } else {
        "modupdater"
    }
}

fn normalize_sha(sha: &str) -> Result<String, String> {
    let lower = sha.to_ascii_lowercase();
    if lower.len() != 64 || !lower.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err("hash_mismatch".into());
    }
    Ok(lower)
}

struct Staged {
    path: PathBuf,
    keep: bool,
}

impl Drop for Staged {
    fn drop(&mut self) {
        if !self.keep {
            let _ = fs::remove_file(&self.path);
        }
    }
}

struct LockGuard {
    path: PathBuf,
    keep: bool,
}

impl Drop for LockGuard {
    fn drop(&mut self) {
        if !self.keep {
            let _ = fs::remove_file(&self.path);
        }
    }
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
            let path = std::env::temp_dir().join(format!("modupdater-upd-{label}-{n}-{nanos}"));
            fs::create_dir_all(&path).unwrap();
            Self { path }
        }
    }

    impl Drop for Tmp {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.path);
        }
    }

    #[test]
    fn version_and_old_path() {
        assert!(valid_client_version("0.1.0"));
        assert!(valid_client_version("1.2.3-rc.1"));
        assert!(!valid_client_version(""));
        assert!(!valid_client_version("01.2.3"));
        assert!(!valid_client_version("1.2"));
        assert!(!valid_client_version("../1.2.3"));
        assert!(!valid_client_version("1.2.3/evil"));
        let exe = Path::new("/opt/modupdater");
        assert_eq!(old_exe_path(exe), PathBuf::from("/opt/modupdater.old"));
        let win = Path::new("C:/app/modupdater.exe");
        assert_eq!(old_exe_path(win).file_name().unwrap(), "modupdater.exe.old");
    }

    #[test]
    fn replace_moves_current_to_old_and_staged_into_place() {
        let tmp = Tmp::new("swap");
        let dir = tmp.path.canonicalize().unwrap();
        let current = dir.join("modupdater");
        let staged = dir.join("staged");
        fs::write(&current, b"old").unwrap();
        fs::write(&staged, b"new").unwrap();
        replace_executable(&current, &staged).unwrap();
        assert_eq!(fs::read(&current).unwrap(), b"new");
        assert_eq!(fs::read(old_exe_path(&current)).unwrap(), b"old");
        assert!(!staged.exists());
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mode = fs::metadata(&current).unwrap().permissions().mode() & 0o777;
            assert_eq!(mode, 0o755);
        }
        fs::write(old_exe_path(&current), b"stale").unwrap();
        cleanup_startup(&current);
        assert!(!old_exe_path(&current).exists());
    }

    #[test]
    fn dev_launch_path_stays_beside_a_separate_directory() {
        let tmp = Tmp::new("devpath");
        let dir = tmp.path.canonicalize().unwrap();
        let exe = dir.join("modupdater");
        fs::write(&exe, b"admin").unwrap();
        let runs = dir.join("dev-runs").join("0.2.0");
        fs::create_dir_all(&runs).unwrap();
        let dest = runs.join(if cfg!(windows) {
            "modupdater.exe"
        } else {
            "modupdater"
        });
        assert_ne!(dest, exe);
        assert!(dest.starts_with(dir.join("dev-runs")));
        assert!(!dest.starts_with(&exe));
    }
}
