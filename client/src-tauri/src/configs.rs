use std::collections::BTreeMap;
use std::fs::{self, File, OpenOptions};
use std::path::{Component, Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

use serde::Serialize;
use zip::{ZipArchive, ZipWriter};

use crate::jars::{self, ct_eq};
use crate::write_private;

const REGISTRY: &str = ".modupdater-configs.json";

#[derive(Debug, Clone, Serialize)]
pub struct PackedZip {
    pub filename: String,
    pub bytes: Vec<u8>,
}

pub fn pack_dir_zip(dir: &Path) -> Result<PackedZip, String> {
    let dir = dir.canonicalize().map_err(|_| "invalid_path".to_string())?;
    if !dir.is_dir() {
        return Err("invalid_path".into());
    }
    let nanos = SystemTime::now().duration_since(UNIX_EPOCH).map_err(|_| "internal".to_string())?.as_nanos();
    let zip_path = std::env::temp_dir().join(format!(".mu-pack-{}-{nanos}.zip", std::process::id()));
    let mut guard = FileGuard { path: zip_path.clone(), keep: false };
    write_dir_zip(&dir, &zip_path)?;
    let bytes = fs::read(&zip_path).map_err(|_| "internal".to_string())?;
    guard.keep = false;
    Ok(PackedZip { filename: zip_download_name(&dir), bytes })
}

pub fn load_registry(root: &Path) -> Result<BTreeMap<String, String>, String> {
    let root = root.canonicalize().map_err(|_| "invalid_config".to_string())?;
    let path = root.join(REGISTRY);
    if is_symlink(&path) {
        return Err("invalid_path".into());
    }
    if !path.exists() {
        return Ok(BTreeMap::new());
    }
    let bytes = fs::read(&path).map_err(|_| "invalid_config".to_string())?;
    serde_json::from_slice(&bytes).map_err(|_| "invalid_config".to_string())
}

pub fn apply_mod_config(
    mods_dir: &Path,
    server_url: &str,
    mod_id: &str,
    config_id: &str,
    dest: &str,
    kind: &str,
    sha256: &str,
    progress: &dyn Fn(u64, u64),
) -> Result<(), String> {
    if !jars::valid_mod_id(mod_id) || !jars::valid_mod_id(config_id) {
        return Err("not_found".into());
    }
    if kind != "file" && kind != "dir" {
        return Err("invalid_config".into());
    }
    let expect = normalize_sha(sha256)?;
    let root = crate::backup::version_root(mods_dir)?;
    let _ = resolve_dest(&root, dest)?;
    let url = jars::api_url(server_url, &format!("/api/mods/{mod_id}/configs/{config_id}/file"))?;
    let temp_dir = std::env::temp_dir().canonicalize().map_err(|_| "internal".to_string())?;
    let temp = jars::create_temp(&temp_dir)?;
    let mut guard = FileGuard { path: temp.clone(), keep: false };
    let hash = jars::download(&url, &temp, server_url, None, progress)?;
    if !ct_eq(&hash, &expect) {
        return Err("hash_mismatch".into());
    }
    install_config_file(&root, config_id, dest, kind, &expect, &temp)?;
    guard.keep = false;
    Ok(())
}

pub fn install_config_file(root: &Path, config_id: &str, dest: &str, kind: &str, sha256: &str, src: &Path) -> Result<(), String> {
    if !jars::valid_mod_id(config_id) {
        return Err("not_found".into());
    }
    if kind != "file" && kind != "dir" {
        return Err("invalid_config".into());
    }
    let expect = normalize_sha(sha256)?;
    let hash = jars::sha256_file(src).map_err(|_| "internal".to_string())?;
    if !ct_eq(&hash, &expect) {
        return Err("hash_mismatch".into());
    }
    let root = root.canonicalize().map_err(|_| "invalid_config".to_string())?;
    let target = resolve_dest(&root, dest)?;
    move_aside(&target)?;
    if kind == "file" {
        if let Some(parent) = target.parent() {
            fs::create_dir_all(parent).map_err(|_| "internal".to_string())?;
        }
        copy_synced(src, &target)?;
    } else {
        fs::create_dir_all(&target).map_err(|_| "internal".to_string())?;
        extract_zip(src, &target)?;
    }
    remember(&root, config_id, &expect)?;
    Ok(())
}

pub fn resolve_dest(root: &Path, dest: &str) -> Result<PathBuf, String> {
    validate_rel(dest)?;
    let root = root.canonicalize().map_err(|_| "invalid_config".to_string())?;
    if !root.is_dir() {
        return Err("invalid_config".into());
    }
    let parts: Vec<&str> = dest.split('/').collect();
    let mut cursor = root.clone();
    for (index, seg) in parts.iter().enumerate() {
        let next = cursor.join(seg);
        match fs::symlink_metadata(&next) {
            Ok(meta) => {
                if meta.file_type().is_symlink() {
                    return Err("invalid_path".into());
                }
                if meta.is_dir() {
                    let canon = next.canonicalize().map_err(|_| "invalid_path".to_string())?;
                    if !canon.starts_with(&root) {
                        return Err("invalid_path".into());
                    }
                    cursor = canon;
                } else if index + 1 != parts.len() {
                    return Err("invalid_path".into());
                } else {
                    if !next.starts_with(&root) {
                        return Err("invalid_path".into());
                    }
                    cursor = next;
                }
            }
            Err(err) if err.kind() == std::io::ErrorKind::NotFound => {
                cursor = next;
                for rest in &parts[index + 1..] {
                    cursor = cursor.join(rest);
                }
                break;
            }
            Err(_) => return Err("invalid_path".into()),
        }
    }
    if cursor.components().any(|component| matches!(component, Component::ParentDir)) || !cursor.starts_with(&root) || cursor == root {
        return Err("invalid_path".into());
    }
    Ok(cursor)
}

fn extract_zip(zip_path: &Path, dest: &Path) -> Result<(), String> {
    let dest = dest.canonicalize().map_err(|_| "invalid_path".to_string())?;
    let file = File::open(zip_path).map_err(|_| "internal".to_string())?;
    let mut archive = ZipArchive::new(file).map_err(|_| "invalid_config".to_string())?;
    for index in 0..archive.len() {
        let mut entry = archive.by_index(index).map_err(|_| "invalid_path".to_string())?;
        if entry.is_symlink() {
            return Err("invalid_path".into());
        }
        let Some(rel) = entry.enclosed_name() else {
            return Err("invalid_path".into());
        };
        if rel.as_os_str().is_empty() {
            continue;
        }
        if rel.components().any(|component| !matches!(component, Component::Normal(_))) {
            return Err("invalid_path".into());
        }
        let out = dest.join(&rel);
        if !out.starts_with(&dest) {
            return Err("invalid_path".into());
        }
        if entry.is_dir() {
            fs::create_dir_all(&out).map_err(|_| "internal".to_string())?;
            continue;
        }
        if let Some(parent) = out.parent() {
            fs::create_dir_all(parent).map_err(|_| "internal".to_string())?;
        }
        let mut outfile = OpenOptions::new().create(true).write(true).truncate(true).open(&out).map_err(|_| "internal".to_string())?;
        std::io::copy(&mut entry, &mut outfile).map_err(|_| "internal".to_string())?;
        outfile.sync_all().map_err(|_| "internal".to_string())?;
    }
    Ok(())
}

fn remember(root: &Path, id: &str, sha: &str) -> Result<(), String> {
    let mut map = load_registry(root)?;
    map.insert(id.to_string(), sha.to_string());
    let bytes = serde_json::to_vec_pretty(&map).map_err(|_| "internal".to_string())?;
    write_private(&root.join(REGISTRY), &bytes)
}

fn move_aside(path: &Path) -> Result<(), String> {
    let meta = match fs::symlink_metadata(path) {
        Ok(meta) => meta,
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(_) => return Err("internal".into()),
    };
    if meta.file_type().is_symlink() {
        return Err("invalid_path".into());
    }
    let name = path.file_name().and_then(|item| item.to_str()).ok_or_else(|| "invalid_path".to_string())?;
    let parent = path.parent().ok_or_else(|| "invalid_path".to_string())?;
    let unix = SystemTime::now().duration_since(UNIX_EPOCH).map_err(|_| "internal".to_string())?.as_secs();
    let bak = next_bak(parent, name, unix)?;
    let dest = parent.join(&bak);
    if dest.parent() != Some(parent) {
        return Err("invalid_path".into());
    }
    fs::rename(path, dest).map_err(|_| "internal".to_string())?;
    Ok(())
}

fn next_bak(dir: &Path, name: &str, unix: u64) -> Result<String, String> {
    let primary = format!("{name}.{unix}.bak");
    if !fs::symlink_metadata(dir.join(&primary)).is_ok() {
        return Ok(primary);
    }
    for n in 2..1000 {
        let candidate = format!("{name}.{unix}-{n}.bak");
        if candidate.len() > 255 {
            return Err("invalid_path".into());
        }
        if fs::symlink_metadata(dir.join(&candidate)).is_err() {
            return Ok(candidate);
        }
    }
    Err("internal".into())
}

fn copy_synced(src: &Path, dest: &Path) -> Result<(), String> {
    let mut input = File::open(src).map_err(|_| "internal".to_string())?;
    let mut output = OpenOptions::new().create(true).write(true).truncate(true).open(dest).map_err(|_| "internal".to_string())?;
    std::io::copy(&mut input, &mut output).map_err(|_| "internal".to_string())?;
    output.sync_all().map_err(|_| "internal".to_string())?;
    Ok(())
}

fn validate_rel(dest: &str) -> Result<(), String> {
    if dest.is_empty() || dest.starts_with('/') || dest.starts_with('\\') {
        return Err("invalid_path".into());
    }
    if dest.contains('\0') || dest.contains(':') || dest.contains('\\') {
        return Err("invalid_path".into());
    }
    let parts: Vec<&str> = dest.split('/').collect();
    if parts.is_empty() || parts.iter().any(|part| part.is_empty() || *part == "." || *part == "..") {
        return Err("invalid_path".into());
    }
    Ok(())
}

fn normalize_sha(sha: &str) -> Result<String, String> {
    let lower = sha.to_ascii_lowercase();
    if lower.len() != 64 || !lower.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err("hash_mismatch".into());
    }
    Ok(lower)
}

pub(crate) fn require_level_dat(dir: &Path) -> Result<(), String> {
    let path = dir.join("level.dat");
    let meta = fs::symlink_metadata(&path).map_err(|_| "В папке нет level.dat.".to_string())?;
    if !meta.file_type().is_file() {
        return Err("В папке нет level.dat.".into());
    }
    Ok(())
}

pub(crate) fn pack_directory(dir: &Path, dest: &Path) -> Result<(), String> {
    write_dir_zip(dir, dest)
}

fn write_dir_zip(dir: &Path, zip_path: &Path) -> Result<(), String> {
    let file = File::create(zip_path).map_err(|_| "internal".to_string())?;
    let mut zip = ZipWriter::new(file);
    let opts = zip::write::SimpleFileOptions::default().compression_method(zip::CompressionMethod::Deflated);
    write_dir_entries(dir, dir, &zip_path, &mut zip, &opts, 0)?;
    zip.finish().map_err(|_| "internal".to_string())?;
    Ok(())
}

fn write_dir_entries(
    root: &Path,
    dir: &Path,
    zip_path: &Path,
    zip: &mut ZipWriter<File>,
    opts: &zip::write::SimpleFileOptions,
    depth: usize,
) -> Result<(), String> {
    if depth > 64 {
        return Err("internal".into());
    }
    let mut entries = fs::read_dir(dir).map_err(|_| "internal".to_string())?.collect::<Result<Vec<_>, _>>().map_err(|_| "internal".to_string())?;
    entries.sort_by_key(|entry| entry.file_name());
    let mut files = 0usize;
    for entry in entries {
        let Some(name) = entry.file_name().to_str().map(str::to_string) else {
            return Err("invalid_path".into());
        };
        if name == "." || name == ".." || name.contains('/') || name.contains('\\') || name.contains('\0') {
            return Err("invalid_path".into());
        }
        let path = dir.join(&name);
        if path == *zip_path {
            continue;
        }
        let file_type = entry.file_type().map_err(|_| "internal".to_string())?;
        if file_type.is_symlink() {
            continue;
        }
        if file_type.is_dir() {
            write_dir_entries(root, &path, zip_path, zip, opts, depth + 1)?;
            files += 1;
            continue;
        }
        if !file_type.is_file() {
            continue;
        }
        let rel = path.strip_prefix(root).map_err(|_| "invalid_path".to_string())?;
        let stored = rel.components().map(|component| component.as_os_str().to_string_lossy().into_owned()).collect::<Vec<_>>().join("/");
        zip.start_file(stored, *opts).map_err(|_| "internal".to_string())?;
        let mut input = File::open(&path).map_err(|_| "internal".to_string())?;
        std::io::copy(&mut input, zip).map_err(|_| "internal".to_string())?;
        files += 1;
    }
    if files == 0 && dir != root {
        let rel = dir.strip_prefix(root).map_err(|_| "invalid_path".to_string())?;
        let stored = rel.components().map(|component| component.as_os_str().to_string_lossy().into_owned()).collect::<Vec<_>>().join("/");
        zip.add_directory(format!("{stored}/"), *opts).map_err(|_| "internal".to_string())?;
    }
    Ok(())
}

fn zip_download_name(dir: &Path) -> String {
    let raw = dir.file_name().and_then(|name| name.to_str()).unwrap_or("config");
    if raw.is_empty() || raw == "." || raw == ".." || raw.contains('/') || raw.contains('\\') || raw.contains('\0') {
        "config.zip".into()
    } else {
        format!("{raw}.zip")
    }
}

fn is_symlink(path: &Path) -> bool {
    fs::symlink_metadata(path).map(|meta| meta.file_type().is_symlink()).unwrap_or(false)
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
    use std::io::Write;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::time::{SystemTime, UNIX_EPOCH};

    struct Tmp {
        path: PathBuf,
    }

    impl Tmp {
        fn new(label: &str) -> Self {
            static N: AtomicUsize = AtomicUsize::new(0);
            let n = N.fetch_add(1, Ordering::Relaxed);
            let nanos = SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos();
            let path = std::env::temp_dir().join(format!("modupdater-cfgapply-{label}-{n}-{nanos}"));
            fs::create_dir_all(&path).unwrap();
            Self { path }
        }
    }

    impl Drop for Tmp {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.path);
        }
    }

    fn digest(data: &[u8]) -> String {
        use sha2::{Digest, Sha256};
        let mut hasher = Sha256::new();
        hasher.update(data);
        jars::to_hex(&hasher.finalize())
    }

    fn id() -> String {
        "ab".repeat(16)
    }

    #[test]
    fn dest_rejects_traversal() {
        let tmp = Tmp::new("dest");
        let root = tmp.path.canonicalize().unwrap();
        assert!(resolve_dest(&root, "../x").is_err());
        assert!(resolve_dest(&root, "/etc/passwd").is_err());
        assert!(resolve_dest(&root, "a/../../b").is_err());
        assert!(resolve_dest(&root, "a/./b").is_err());
        assert!(resolve_dest(&root, "C:/Windows").is_err());
        assert!(resolve_dest(&root, "").is_err());
        assert!(resolve_dest(&root, "config/jei/jei.toml").is_ok());
    }

    #[test]
    fn apply_file_baks_existing_and_records_hash() {
        let tmp = Tmp::new("apply");
        let root = tmp.path.join("instance");
        fs::create_dir_all(root.join("config/jei")).unwrap();
        fs::create_dir_all(root.join("mods")).unwrap();
        fs::write(root.join("config/jei/jei.toml"), b"old").unwrap();
        let src = tmp.path.join("new.toml");
        fs::write(&src, b"new-config").unwrap();
        let root = root.canonicalize().unwrap();
        install_config_file(&root, &id(), "config/jei/jei.toml", "file", &digest(b"new-config"), &src).unwrap();
        assert_eq!(fs::read(root.join("config/jei/jei.toml")).unwrap(), b"new-config");
        let names = fs::read_dir(root.join("config/jei")).unwrap().map(|entry| entry.unwrap().file_name().to_string_lossy().into_owned()).collect::<Vec<_>>();
        assert!(names.iter().any(|name| name.starts_with("jei.toml.") && name.ends_with(".bak")));
        let bak = names.iter().find(|name| name.ends_with(".bak")).unwrap();
        assert_eq!(fs::read(root.join("config/jei").join(bak)).unwrap(), b"old");
        let map = load_registry(&root).unwrap();
        assert_eq!(map.get(&id()).unwrap(), &digest(b"new-config"));
    }

    #[test]
    fn apply_dir_extracts_and_rejects_zip_slip() {
        let tmp = Tmp::new("dir");
        let root = tmp.path.join("instance");
        fs::create_dir_all(root.join("shaderpacks/Complementary")).unwrap();
        fs::write(root.join("shaderpacks/Complementary/old.txt"), b"old").unwrap();
        let zip_path = tmp.path.join("pack.zip");
        {
            let file = File::create(&zip_path).unwrap();
            let mut zip = zip::ZipWriter::new(file);
            let opts = zip::write::SimpleFileOptions::default();
            zip.start_file("shaders/a.txt", opts).unwrap();
            zip.write_all(b"shader").unwrap();
            zip.finish().unwrap();
        }
        let root = root.canonicalize().unwrap();
        let bytes = fs::read(&zip_path).unwrap();
        install_config_file(&root, &id(), "shaderpacks/Complementary", "dir", &digest(&bytes), &zip_path).unwrap();
        assert_eq!(fs::read(root.join("shaderpacks/Complementary/shaders/a.txt")).unwrap(), b"shader");
        let parent = root.join("shaderpacks");
        let names = fs::read_dir(&parent).unwrap().map(|entry| entry.unwrap().file_name().to_string_lossy().into_owned()).collect::<Vec<_>>();
        assert!(names.iter().any(|name| name.starts_with("Complementary.") && name.ends_with(".bak")));

        let slip = tmp.path.join("slip.zip");
        {
            let file = File::create(&slip).unwrap();
            let mut zip = zip::ZipWriter::new(file);
            let opts = zip::write::SimpleFileOptions::default();
            zip.start_file("../evil.txt", opts).unwrap();
            zip.write_all(b"nope").unwrap();
            zip.finish().unwrap();
        }
        let dest = root.join("shaderpacks/out");
        fs::create_dir_all(&dest).unwrap();
        let err = extract_zip(&slip, &dest).unwrap_err();
        assert_eq!(err, "invalid_path");
        assert!(!root.join("evil.txt").exists());
        assert!(!tmp.path.join("evil.txt").exists());
    }

    #[test]
    fn pack_dir_uses_relative_names_and_skips_symlinks() {
        let tmp = Tmp::new("pack");
        let dir = tmp.path.join("Complementary");
        fs::create_dir_all(dir.join("shaders")).unwrap();
        fs::write(dir.join("shaders/a.txt"), b"shader").unwrap();
        #[cfg(unix)]
        std::os::unix::fs::symlink(dir.join("shaders/a.txt"), dir.join("link.txt")).unwrap();
        let packed = pack_dir_zip(&dir).unwrap();
        assert_eq!(packed.filename, "Complementary.zip");
        let cursor = std::io::Cursor::new(packed.bytes);
        let mut archive = zip::ZipArchive::new(cursor).unwrap();
        let mut names = Vec::new();
        for index in 0..archive.len() {
            names.push(archive.by_index(index).unwrap().name().to_string());
        }
        assert!(names.iter().any(|name| name == "shaders/a.txt"));
        assert!(names.iter().all(|name| name != "link.txt"));
        assert!(names.iter().all(|name| !name.contains("..")));
    }

    #[test]
    fn world_pack_keeps_level_dat_at_zip_root() {
        let tmp = Tmp::new("world");
        let dir = tmp.path.join("New World");
        fs::create_dir_all(dir.join("region")).unwrap();
        fs::write(dir.join("level.dat"), b"level").unwrap();
        fs::write(dir.join("region/r.0.0.mca"), b"chunk").unwrap();
        assert!(require_level_dat(&dir).is_ok());
        assert!(require_level_dat(&tmp.path).is_err());
        let zip_path = tmp.path.join("world.zip");
        pack_directory(&dir, &zip_path).unwrap();
        let file = File::open(&zip_path).unwrap();
        let mut archive = zip::ZipArchive::new(file).unwrap();
        let mut names = Vec::new();
        for index in 0..archive.len() {
            names.push(archive.by_index(index).unwrap().name().to_string());
        }
        assert!(names.iter().any(|name| name == "level.dat"));
        assert!(names.iter().any(|name| name == "region/r.0.0.mca"));
    }
}
