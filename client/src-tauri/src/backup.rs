use std::fs::{self, File, OpenOptions};
use std::io::{Read, Write};
use std::path::{Component, Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

use serde::Serialize;
use zip::write::SimpleFileOptions;
use zip::ZipWriter;

const BACKUP_PREFIX: &str = "modupdater-initial-backup-";
const MARKER: &str = ".modupdater-backup-done";

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct BackupOutcome {
    pub created: bool,
    pub path: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct LauncherInfo {
    pub kind: String,
    pub root: String,
    pub current_version: String,
    pub versions: Vec<String>,
}

pub fn version_root(mods_dir: &Path) -> Result<PathBuf, String> {
    let mods = mods_dir
        .canonicalize()
        .map_err(|_| "invalid_config".to_string())?;
    if !mods.is_dir() {
        return Err("invalid_config".into());
    }
    let parent = mods.parent().ok_or_else(|| "invalid_config".to_string())?;
    if parent.as_os_str().is_empty() || normal_components(parent) == 0 {
        return Err("invalid_config".into());
    }
    Ok(parent.to_path_buf())
}

pub fn ensure_instance_backup(
    root: &Path,
    progress: &dyn Fn(u64, u64),
) -> Result<BackupOutcome, String> {
    let root = canonical_dir(root)?;
    let marker = root.join(MARKER);
    if is_symlink(&marker) {
        return Err("invalid_path".into());
    }
    if marker.is_file() {
        return Ok(BackupOutcome {
            created: false,
            path: String::new(),
        });
    }
    let path = write_backup_zip(&root, progress)?;
    let name = path
        .file_name()
        .and_then(|item| item.to_str())
        .unwrap_or("backup.zip");
    write_marker(&marker, name.as_bytes())?;
    Ok(BackupOutcome {
        created: true,
        path: path.to_string_lossy().into_owned(),
    })
}

pub fn create_instance_backup(
    root: &Path,
    progress: &dyn Fn(u64, u64),
) -> Result<BackupOutcome, String> {
    let root = canonical_dir(root)?;
    let path = write_backup_zip(&root, progress)?;
    Ok(BackupOutcome {
        created: true,
        path: path.to_string_lossy().into_owned(),
    })
}

pub fn inspect_launcher(mods_dir: &Path) -> Result<LauncherInfo, String> {
    let mods = mods_dir
        .canonicalize()
        .map_err(|_| "invalid_config".to_string())?;
    let fallback_root = version_root(&mods)?;
    let other = LauncherInfo {
        kind: "other".into(),
        root: fallback_root.to_string_lossy().into_owned(),
        current_version: String::new(),
        versions: Vec::new(),
    };
    if mods.file_name().and_then(|name| name.to_str()) != Some("mods") {
        return Ok(other);
    }
    let version_dir = mods.parent().ok_or_else(|| "invalid_config".to_string())?;
    let home = version_dir
        .parent()
        .ok_or_else(|| "invalid_config".to_string())?;
    if home.file_name().and_then(|name| name.to_str()) != Some("home") {
        return Ok(other);
    }
    let Some(version_name) = version_dir.file_name().and_then(|name| name.to_str()) else {
        return Ok(other);
    };
    if !valid_version_name(version_name) {
        return Ok(other);
    }
    let mc_root = home.parent().ok_or_else(|| "invalid_config".to_string())?;
    if normal_components(mc_root) == 0 {
        return Ok(other);
    }
    let versions_dir = mc_root.join("versions").join(version_name);
    if !versions_dir.is_dir() || is_symlink(&versions_dir) {
        return Ok(other);
    }
    let mut versions = list_versions(&mc_root.join("versions"))?;
    if !versions.iter().any(|name| name == version_name) {
        versions.push(version_name.to_string());
        versions.sort();
    }
    Ok(LauncherInfo {
        kind: "legacy".into(),
        root: mc_root.to_string_lossy().into_owned(),
        current_version: version_name.to_string(),
        versions,
    })
}

pub fn duplicate_version(
    mods_dir: &Path,
    src: &str,
    new_name: &str,
    progress: &dyn Fn(u64, u64),
) -> Result<(), String> {
    let info = inspect_launcher(mods_dir)?;
    if info.kind != "legacy" {
        return Err("not_legacy".into());
    }
    if !valid_version_name(src) || !valid_version_name(new_name) {
        return Err("invalid_path".into());
    }
    let mc_root = PathBuf::from(&info.root);
    let versions_src = mc_root.join("versions").join(src);
    let home_src = mc_root.join("home").join(src);
    let versions_dest = mc_root.join("versions").join(new_name);
    let home_dest = mc_root.join("home").join(new_name);
    if !versions_src.is_dir() || !home_src.is_dir() {
        return Err("not_found".into());
    }
    if entry_exists(&versions_dest) || entry_exists(&home_dest) {
        return Err("already_exists".into());
    }
    let result = (|| {
        copy_tree(&versions_src, &versions_dest, &|_, _| {})?;
        rename_version_files(&versions_dest, src, new_name)?;
        patch_version_id(&versions_dest.join(format!("{new_name}.json")), new_name)?;
        copy_tree(&home_src, &home_dest, progress)?;
        Ok(())
    })();
    if result.is_err() {
        if path_inside(&mc_root, &versions_dest) {
            let _ = fs::remove_dir_all(&versions_dest);
        }
        if path_inside(&mc_root, &home_dest) {
            let _ = fs::remove_dir_all(&home_dest);
        }
    }
    result
}

fn write_backup_zip(root: &Path, progress: &dyn Fn(u64, u64)) -> Result<PathBuf, String> {
    let dest = backup_zip_path(root)?;
    let entries = collect_entries(root, &dest)?;
    let total = entries.iter().map(|entry| entry.bytes).sum::<u64>();
    progress(0, total);
    let mut guard = FileGuard {
        path: dest.clone(),
        keep: false,
    };
    let file = File::create(&dest).map_err(|_| "internal".to_string())?;
    let mut zip = ZipWriter::new(file);
    let opts = SimpleFileOptions::default().compression_method(zip::CompressionMethod::Deflated);
    let mut loaded = 0u64;
    for entry in &entries {
        let name = &entry.name;
        if entry.dir {
            let dir_name = if name.ends_with('/') {
                name.clone()
            } else {
                format!("{name}/")
            };
            zip.add_directory(dir_name, opts)
                .map_err(|_| "internal".to_string())?;
            continue;
        }
        zip.start_file(name, opts)
            .map_err(|_| "internal".to_string())?;
        let mut input = File::open(&entry.path).map_err(|_| "internal".to_string())?;
        let mut buf = [0u8; 64 * 1024];
        loop {
            let n = input.read(&mut buf).map_err(|_| "internal".to_string())?;
            if n == 0 {
                break;
            }
            zip.write_all(&buf[..n])
                .map_err(|_| "internal".to_string())?;
            loaded += n as u64;
            progress(loaded.min(total), total);
        }
    }
    let finished = zip.finish().map_err(|_| "internal".to_string())?;
    finished.sync_all().map_err(|_| "internal".to_string())?;
    progress(total, total);
    guard.keep = true;
    Ok(dest)
}

struct Planned {
    name: String,
    path: PathBuf,
    dir: bool,
    bytes: u64,
}

fn collect_entries(root: &Path, zip_path: &Path) -> Result<Vec<Planned>, String> {
    let mut out = Vec::new();
    walk_entries(root, root, zip_path, &mut out)?;
    Ok(out)
}

fn walk_entries(
    root: &Path,
    dir: &Path,
    zip_path: &Path,
    out: &mut Vec<Planned>,
) -> Result<(), String> {
    let mut names = fs::read_dir(dir)
        .map_err(|_| "internal".to_string())?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|_| "internal".to_string())?;
    names.sort_by_key(|entry| entry.file_name());
    for entry in names {
        let name = entry.file_name();
        let Some(name) = name.to_str() else {
            continue;
        };
        if !segment_ok(name) {
            return Err("invalid_path".into());
        }
        let path = dir.join(name);
        if path == *zip_path {
            continue;
        }
        let file_type = entry.file_type().map_err(|_| "internal".to_string())?;
        if file_type.is_symlink() {
            continue;
        }
        let at_root = dir == root;
        if at_root && (name == "logs" || name == "crash-reports") {
            continue;
        }
        if at_root && name.starts_with(BACKUP_PREFIX) && name.ends_with(".zip") {
            continue;
        }
        if file_type.is_dir() {
            let before = out.len();
            walk_entries(root, &path, zip_path, out)?;
            if out.len() == before {
                out.push(Planned {
                    name: rel_name(root, &path)?,
                    path,
                    dir: true,
                    bytes: 0,
                });
            }
            continue;
        }
        if !file_type.is_file() {
            continue;
        }
        let meta = entry.metadata().map_err(|_| "internal".to_string())?;
        out.push(Planned {
            name: rel_name(root, &path)?,
            path,
            dir: false,
            bytes: meta.len(),
        });
    }
    Ok(())
}

fn rel_name(root: &Path, path: &Path) -> Result<String, String> {
    let rel = path
        .strip_prefix(root)
        .map_err(|_| "invalid_path".to_string())?;
    let mut parts = Vec::new();
    for component in rel.components() {
        match component {
            Component::Normal(name) => {
                let text = name.to_str().ok_or_else(|| "invalid_path".to_string())?;
                if !segment_ok(text) {
                    return Err("invalid_path".into());
                }
                parts.push(text.to_string());
            }
            _ => return Err("invalid_path".into()),
        }
    }
    if parts.is_empty() {
        return Err("invalid_path".into());
    }
    Ok(parts.join("/"))
}

fn backup_zip_path(root: &Path) -> Result<PathBuf, String> {
    let unix = unix_secs()?;
    let primary = root.join(format!("{BACKUP_PREFIX}{unix}.zip"));
    if !entry_exists(&primary) {
        return Ok(primary);
    }
    for n in 2..1000 {
        let path = root.join(format!("{BACKUP_PREFIX}{unix}-{n}.zip"));
        if !entry_exists(&path) {
            return Ok(path);
        }
    }
    Err("internal".into())
}

fn copy_tree(src: &Path, dest: &Path, progress: &dyn Fn(u64, u64)) -> Result<(), String> {
    let src = src.canonicalize().map_err(|_| "not_found".to_string())?;
    if dest.exists() {
        return Err("already_exists".into());
    }
    let total = tree_bytes(&src)?;
    progress(0, total);
    let mut loaded = 0u64;
    copy_tree_inner(&src, dest, &mut loaded, total, progress, 0)?;
    progress(total, total);
    Ok(())
}

fn copy_tree_inner(
    src: &Path,
    dest: &Path,
    loaded: &mut u64,
    total: u64,
    progress: &dyn Fn(u64, u64),
    depth: usize,
) -> Result<(), String> {
    if depth > 64 {
        return Err("internal".into());
    }
    fs::create_dir(dest).map_err(|_| "internal".to_string())?;
    let mut entries = fs::read_dir(src)
        .map_err(|_| "internal".to_string())?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|_| "internal".to_string())?;
    entries.sort_by_key(|entry| entry.file_name());
    for entry in entries {
        let name = entry.file_name();
        let Some(name) = name.to_str() else {
            return Err("invalid_path".into());
        };
        if !segment_ok(name) {
            return Err("invalid_path".into());
        }
        let from = src.join(name);
        let to = dest.join(name);
        if !path_inside(src, &from) || to.parent() != Some(dest) {
            return Err("invalid_path".into());
        }
        let file_type = entry.file_type().map_err(|_| "internal".to_string())?;
        if file_type.is_symlink() {
            continue;
        }
        if file_type.is_dir() {
            copy_tree_inner(&from, &to, loaded, total, progress, depth + 1)?;
            continue;
        }
        if !file_type.is_file() {
            continue;
        }
        let bytes = fs::copy(&from, &to).map_err(|_| "internal".to_string())?;
        *loaded += bytes;
        progress((*loaded).min(total), total);
    }
    Ok(())
}

fn tree_bytes(dir: &Path) -> Result<u64, String> {
    let mut total = 0u64;
    let mut stack = vec![dir.to_path_buf()];
    let mut depth = 0usize;
    while let Some(current) = stack.pop() {
        depth += 1;
        if depth > 100_000 {
            return Err("internal".into());
        }
        let entries = fs::read_dir(&current).map_err(|_| "internal".to_string())?;
        for entry in entries {
            let entry = entry.map_err(|_| "internal".to_string())?;
            let file_type = entry.file_type().map_err(|_| "internal".to_string())?;
            if file_type.is_symlink() {
                continue;
            }
            if file_type.is_dir() {
                stack.push(entry.path());
                continue;
            }
            if file_type.is_file() {
                let meta = entry.metadata().map_err(|_| "internal".to_string())?;
                total += meta.len();
            }
        }
    }
    Ok(total)
}

fn rename_version_files(dir: &Path, src: &str, new_name: &str) -> Result<(), String> {
    for ext in ["jar", "json"] {
        let from = dir.join(format!("{src}.{ext}"));
        let to = dir.join(format!("{new_name}.{ext}"));
        if !from.is_file() || is_symlink(&from) {
            return Err("not_found".into());
        }
        if entry_exists(&to) {
            return Err("already_exists".into());
        }
        fs::rename(&from, &to).map_err(|_| "internal".to_string())?;
    }
    Ok(())
}

fn patch_version_id(path: &Path, new_name: &str) -> Result<(), String> {
    if is_symlink(path) || !path.is_file() {
        return Err("not_found".into());
    }
    let bytes = fs::read(path).map_err(|_| "internal".to_string())?;
    if bytes.len() > 1024 * 1024 {
        return Err("internal".into());
    }
    let mut value: serde_json::Value =
        serde_json::from_slice(&bytes).map_err(|_| "invalid_config".to_string())?;
    let obj = value
        .as_object_mut()
        .ok_or_else(|| "invalid_config".to_string())?;
    obj.insert("id".into(), serde_json::Value::String(new_name.to_string()));
    let mut out = serde_json::to_vec_pretty(&value).map_err(|_| "internal".to_string())?;
    out.push(b'\n');
    let mut file = OpenOptions::new()
        .write(true)
        .truncate(true)
        .open(path)
        .map_err(|_| "internal".to_string())?;
    file.write_all(&out).map_err(|_| "internal".to_string())?;
    file.sync_all().map_err(|_| "internal".to_string())?;
    Ok(())
}

fn list_versions(dir: &Path) -> Result<Vec<String>, String> {
    if !dir.is_dir() {
        return Ok(Vec::new());
    }
    let mut names = Vec::new();
    for entry in fs::read_dir(dir).map_err(|_| "internal".to_string())? {
        let entry = entry.map_err(|_| "internal".to_string())?;
        let file_type = entry.file_type().map_err(|_| "internal".to_string())?;
        if file_type.is_symlink() || !file_type.is_dir() {
            continue;
        }
        let file_name = entry.file_name();
        let Some(name) = file_name.to_str() else {
            continue;
        };
        if valid_version_name(name) {
            names.push(name.to_string());
        }
    }
    names.sort();
    Ok(names)
}

pub(crate) fn valid_version_name(name: &str) -> bool {
    if name.is_empty() || name.len() > 200 || name != name.trim() {
        return false;
    }
    if name.ends_with('.') || name == "." || name == ".." || name.contains("..") {
        return false;
    }
    !name.bytes().any(|byte| {
        byte == 0 || byte == b'/' || byte == b'\\' || byte == b':' || byte < 0x20 || byte == 0x7f
    })
}

fn segment_ok(name: &str) -> bool {
    !name.is_empty()
        && name != "."
        && name != ".."
        && !name.contains('/')
        && !name.contains('\\')
        && !name.contains('\0')
}

fn canonical_dir(path: &Path) -> Result<PathBuf, String> {
    let path = path
        .canonicalize()
        .map_err(|_| "invalid_config".to_string())?;
    if !path.is_dir() || normal_components(&path) == 0 {
        return Err("invalid_config".into());
    }
    Ok(path)
}

fn normal_components(path: &Path) -> usize {
    path.components()
        .filter(|component| matches!(component, Component::Normal(_)))
        .count()
}

fn path_inside(root: &Path, path: &Path) -> bool {
    path.starts_with(root) && path != root
}

fn entry_exists(path: &Path) -> bool {
    fs::symlink_metadata(path).is_ok()
}

fn is_symlink(path: &Path) -> bool {
    fs::symlink_metadata(path)
        .map(|meta| meta.file_type().is_symlink())
        .unwrap_or(false)
}

fn unix_secs() -> Result<u64, String> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .map_err(|_| "internal".to_string())
}

fn write_marker(path: &Path, bytes: &[u8]) -> Result<(), String> {
    if is_symlink(path) {
        return Err("invalid_path".into());
    }
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(path)
        .map_err(|_| "internal".to_string())?;
    file.write_all(bytes).map_err(|_| "internal".to_string())?;
    file.sync_all().map_err(|_| "internal".to_string())?;
    Ok(())
}

struct FileGuard {
    path: PathBuf,
    keep: bool,
}

impl Drop for FileGuard {
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
            let path = std::env::temp_dir().join(format!("modupdater-bak-{label}-{n}-{nanos}"));
            fs::create_dir_all(&path).unwrap();
            Self { path }
        }
    }

    impl Drop for Tmp {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.path);
        }
    }

    fn zip_names(path: &Path) -> Vec<String> {
        let file = File::open(path).unwrap();
        let mut archive = zip::ZipArchive::new(file).unwrap();
        let mut names = Vec::new();
        for index in 0..archive.len() {
            names.push(
                archive
                    .by_index(index)
                    .unwrap()
                    .name()
                    .trim_end_matches('/')
                    .to_string(),
            );
        }
        names.sort();
        names
    }

    #[test]
    fn backup_skips_logs_crash_reports_and_previous_zips() {
        let tmp = Tmp::new("zip");
        let root = tmp.path.join("instance");
        fs::create_dir_all(root.join("mods")).unwrap();
        fs::create_dir_all(root.join("saves/world")).unwrap();
        fs::create_dir_all(root.join("config")).unwrap();
        fs::create_dir_all(root.join("logs")).unwrap();
        fs::create_dir_all(root.join("crash-reports")).unwrap();
        fs::create_dir_all(root.join("nested/logs")).unwrap();
        fs::write(root.join("mods/a.jar"), b"jar").unwrap();
        fs::write(root.join("saves/world/level.dat"), b"world").unwrap();
        fs::write(root.join("config/x.txt"), b"cfg").unwrap();
        fs::write(root.join("options.txt"), b"opt").unwrap();
        fs::write(root.join("logs/latest.log"), b"log").unwrap();
        fs::write(root.join("crash-reports/crash.txt"), b"crash").unwrap();
        fs::write(root.join("modupdater-initial-backup-1.zip"), b"old").unwrap();
        fs::write(root.join("nested/logs/keep.txt"), b"keep").unwrap();
        #[cfg(unix)]
        std::os::unix::fs::symlink(root.join("options.txt"), root.join("link.txt")).unwrap();

        let first = ensure_instance_backup(&root, &|_, _| {}).unwrap();
        assert!(first.created);
        assert!(root.join(MARKER).is_file());
        let names = zip_names(Path::new(&first.path));
        assert!(names.iter().any(|name| name == "mods/a.jar"));
        assert!(names.iter().any(|name| name == "saves/world/level.dat"));
        assert!(names.iter().any(|name| name == "config/x.txt"));
        assert!(names.iter().any(|name| name == "options.txt"));
        assert!(names.iter().any(|name| name == "nested/logs/keep.txt"));
        assert!(names
            .iter()
            .all(|name| !name.starts_with("logs") && !name.starts_with("crash-reports")));
        assert!(names
            .iter()
            .all(|name| !name.contains("modupdater-initial-backup")));
        assert!(names.iter().all(|name| name != "link.txt"));
        assert!(!names.iter().any(|name| name.contains("..")));

        let second = ensure_instance_backup(&root, &|_, _| {}).unwrap();
        assert!(!second.created);
        let zips = fs::read_dir(&root)
            .unwrap()
            .filter(|entry| {
                entry
                    .as_ref()
                    .unwrap()
                    .file_name()
                    .to_string_lossy()
                    .starts_with(BACKUP_PREFIX)
            })
            .count();
        assert_eq!(zips, 2);

        let third = create_instance_backup(&root, &|_, _| {}).unwrap();
        assert!(third.created);
        assert!(Path::new(&third.path).is_file());
    }

    #[test]
    fn refuses_to_backup_filesystem_root() {
        let err = version_root(Path::new("/")).unwrap_err();
        assert_eq!(err, "invalid_config");
    }

    #[test]
    fn inspect_legacy_and_other() {
        let tmp = Tmp::new("inspect");
        let mc = tmp.path.join(".minecraft");
        let version = "1.20.1 foo";
        fs::create_dir_all(mc.join("home").join(version).join("mods")).unwrap();
        fs::create_dir_all(mc.join("versions").join(version)).unwrap();
        fs::create_dir_all(mc.join("versions").join("other")).unwrap();
        let mods = mc.join("home").join(version).join("mods");
        let info = inspect_launcher(&mods).unwrap();
        assert_eq!(info.kind, "legacy");
        assert_eq!(info.current_version, version);
        assert_eq!(
            info.versions,
            vec!["1.20.1 foo".to_string(), "other".to_string()]
        );
        assert_eq!(PathBuf::from(&info.root), mc.canonicalize().unwrap());

        let plain = tmp.path.join("instance/mods");
        fs::create_dir_all(&plain).unwrap();
        let other = inspect_launcher(&plain).unwrap();
        assert_eq!(other.kind, "other");
        assert!(other.current_version.is_empty());
        assert!(other.versions.is_empty());
        assert_eq!(
            PathBuf::from(&other.root),
            plain.parent().unwrap().canonicalize().unwrap()
        );
    }

    #[test]
    fn duplicate_copies_both_trees_and_rejects_existing() {
        let tmp = Tmp::new("dup");
        let mc = tmp.path.join(".minecraft");
        let src = "fabric";
        fs::create_dir_all(mc.join("home").join(src).join("mods")).unwrap();
        fs::create_dir_all(mc.join("home").join(src).join("saves")).unwrap();
        fs::write(mc.join("home").join(src).join("options.txt"), b"opt").unwrap();
        fs::write(mc.join("home").join(src).join("mods/a.jar"), b"mod").unwrap();
        let ver = mc.join("versions").join(src);
        fs::create_dir_all(ver.join("natives")).unwrap();
        fs::write(ver.join("fabric.jar"), b"bin").unwrap();
        fs::write(ver.join("natives/lib.bin"), b"nat").unwrap();
        fs::write(ver.join("fabric.json"), br#"{"id":"fabric","time":"t"}"#).unwrap();
        let mods = mc.join("home").join(src).join("mods");
        let new_name = "fabric (copy 1)";
        duplicate_version(&mods, src, new_name, &|_, _| {}).unwrap();
        let copied = mc.join("versions").join(new_name);
        assert!(copied.join("fabric (copy 1).jar").is_file());
        assert!(!copied.join("fabric.jar").exists());
        assert_eq!(fs::read(copied.join("natives/lib.bin")).unwrap(), b"nat");
        let json: serde_json::Value =
            serde_json::from_slice(&fs::read(copied.join("fabric (copy 1).json")).unwrap())
                .unwrap();
        assert_eq!(json["id"], new_name);
        assert_eq!(json["time"], "t");
        assert_eq!(
            fs::read(mc.join("home").join(new_name).join("options.txt")).unwrap(),
            b"opt"
        );
        assert_eq!(
            fs::read(mc.join("home").join(new_name).join("mods/a.jar")).unwrap(),
            b"mod"
        );
        let err = duplicate_version(&mods, src, new_name, &|_, _| {}).unwrap_err();
        assert_eq!(err, "already_exists");
        assert!(mc.join("home").join(src).join("options.txt").is_file());
    }

    #[test]
    fn duplicate_rejects_traversal_and_non_legacy() {
        let tmp = Tmp::new("trav");
        let mc = tmp.path.join(".minecraft");
        fs::create_dir_all(mc.join("home/fabric/mods")).unwrap();
        fs::create_dir_all(mc.join("versions/fabric")).unwrap();
        fs::write(mc.join("versions/fabric/fabric.jar"), b"b").unwrap();
        fs::write(
            mc.join("versions/fabric/fabric.json"),
            br#"{"id":"fabric"}"#,
        )
        .unwrap();
        let mods = mc.join("home/fabric/mods");
        assert_eq!(
            duplicate_version(&mods, "fabric", "../evil", &|_, _| {}).unwrap_err(),
            "invalid_path"
        );
        assert_eq!(
            duplicate_version(&mods, "fabric", "evil/name", &|_, _| {}).unwrap_err(),
            "invalid_path"
        );
        assert!(!tmp.path.join("evil").exists());
        let plain = tmp.path.join("mods");
        fs::create_dir_all(&plain).unwrap();
        assert_eq!(
            duplicate_version(&plain, "a", "b", &|_, _| {}).unwrap_err(),
            "not_legacy"
        );
    }

    #[test]
    fn backup_progress_is_file_bytes() {
        let tmp = Tmp::new("prog");
        let root = tmp.path.join("instance");
        fs::create_dir_all(root.join("mods")).unwrap();
        fs::write(root.join("mods/a.jar"), vec![1u8; 1000]).unwrap();
        fs::write(root.join("options.txt"), b"abcd").unwrap();
        fs::create_dir_all(root.join("logs")).unwrap();
        fs::write(root.join("logs/latest.log"), b"nope").unwrap();
        let samples = std::sync::Mutex::new(Vec::new());
        let out = create_instance_backup(&root, &|loaded, total| {
            samples.lock().unwrap().push((loaded, total));
        })
        .unwrap();
        let samples = samples.lock().unwrap();
        assert!(out.created);
        assert!(root.join("options.txt").is_file());
        assert!(root.join("logs/latest.log").is_file());
        let total = samples[0].1;
        assert_eq!(total, 1004);
        assert_eq!(samples[0], (0, total));
        assert_eq!(*samples.last().unwrap(), (total, total));
        for pair in samples.windows(2) {
            assert!(pair[1].0 >= pair[0].0);
            assert_eq!(pair[1].1, total);
        }
        let names = zip_names(Path::new(&out.path));
        assert!(names.iter().any(|name| name == "mods/a.jar"));
        assert!(names.iter().any(|name| name == "options.txt"));
        assert!(names.iter().all(|name| !name.starts_with("logs")));
    }
}
