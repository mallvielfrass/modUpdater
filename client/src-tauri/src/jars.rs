use std::fs::{self, File, OpenOptions};
use std::io::{Read, Write};
use std::path::{Path, PathBuf};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use sha2::{Digest, Sha256};

use crate::validate_server_url;

#[cfg(test)]
use crate::modlib::next_bak_name;
pub use crate::modlib::safe_jar_name;
pub(crate) use crate::modlib::valid_mod_id;
use crate::modlib::{join_segment, read_slug_version, rename_to_bak, ReadMeta};

#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct LocalJar {
    pub filename: String,
    pub sha256: String,
    pub size: u64,
    pub slug: String,
    pub version: String,
}

pub fn scan_dir(mods_dir: &Path) -> Result<Vec<LocalJar>, String> {
    let dir = mods_dir
        .canonicalize()
        .map_err(|_| "invalid_config".to_string())?;
    if !dir.is_dir() {
        return Err("invalid_config".into());
    }
    let mut out = Vec::new();
    for entry in fs::read_dir(&dir).map_err(|_| "invalid_config".to_string())? {
        let entry = entry.map_err(|_| "invalid_config".to_string())?;
        let Some(name) = entry.file_name().into_string().ok() else {
            continue;
        };
        let file_type = entry
            .file_type()
            .map_err(|_| "invalid_config".to_string())?;
        if file_type.is_symlink() || file_type.is_dir() || !file_type.is_file() {
            continue;
        }
        match safe_jar_name(&name) {
            Ok(clean) if clean == name => {}
            _ => continue,
        }
        let path = match join_segment(&dir, &name) {
            Ok(path) => path,
            Err(_) => continue,
        };
        let meta = match fs::symlink_metadata(&path) {
            Ok(meta) => meta,
            Err(_) => continue,
        };
        if !meta.file_type().is_file() || meta.len() == 0 {
            continue;
        }
        let sha256 = match sha256_file(&path) {
            Ok(hash) => hash,
            Err(_) => continue,
        };
        let stem = name.strip_suffix(".jar").unwrap_or(&name);
        let (slug, version) = match read_slug_version(&path, stem) {
            Ok(meta) => meta,
            Err(ReadMeta::Skip) => continue,
        };
        out.push(LocalJar {
            filename: name,
            sha256,
            size: meta.len(),
            slug,
            version,
        });
    }
    out.sort_by(|left, right| left.filename.cmp(&right.filename));
    Ok(out)
}

pub fn install_jar(
    mods_dir: &Path,
    server_url: &str,
    mod_id: &str,
    filename: &str,
    sha256: &str,
    backup_filename: Option<&str>,
    progress: &dyn Fn(u64, u64),
) -> Result<(), String> {
    if !valid_mod_id(mod_id) {
        return Err("not_found".into());
    }
    if has_separator(filename) {
        return Err("invalid_filename".into());
    }
    let filename = safe_jar_name(filename)?;
    let backup = match backup_filename {
        Some(name) if !name.is_empty() => {
            if has_separator(name) {
                return Err("invalid_filename".into());
            }
            Some(safe_jar_name(name)?)
        }
        _ => None,
    };
    let url = file_url(server_url, mod_id)?;
    let dir = mods_dir
        .canonicalize()
        .map_err(|_| "invalid_config".to_string())?;
    if !dir.is_dir() {
        return Err("invalid_config".into());
    }
    let temp = create_temp(&dir)?;
    let mut guard = TempGuard {
        path: temp,
        keep: false,
    };
    let hash = download(&url, &guard.path, server_url, None, progress)?;
    if !ct_eq(&hash, sha256) {
        return Err("hash_mismatch".into());
    }
    if let Some(name) = backup.as_deref() {
        if name != filename {
            rename_to_bak(&dir, name)?;
        }
    }
    let target = join_segment(&dir, &filename)?;
    if fs::symlink_metadata(&target).is_ok() {
        rename_to_bak(&dir, &filename)?;
    }
    fs::rename(&guard.path, &target).map_err(|_| "internal".to_string())?;
    guard.keep = true;
    Ok(())
}

pub fn backup_jar(mods_dir: &Path, filename: &str) -> Result<(), String> {
    if has_separator(filename) {
        return Err("invalid_filename".into());
    }
    let filename = safe_jar_name(filename)?;
    let dir = mods_dir
        .canonicalize()
        .map_err(|_| "invalid_config".to_string())?;
    rename_to_bak(&dir, &filename)
}

fn api_error_text(code: &str) -> Option<String> {
    if code.is_empty() || code.len() > 180 || code.contains('\n') || code.contains('\r') {
        return None;
    }
    let machine = code.len() <= 64
        && code
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte == b'_');
    let sentence = code.chars().any(|ch| !ch.is_ascii());
    if machine || sentence {
        return Some(code.to_string());
    }
    None
}

fn has_separator(name: &str) -> bool {
    name.contains('/') || name.contains('\\') || name.contains('\0')
}

pub(crate) fn api_url(server_url: &str, path: &str) -> Result<reqwest::Url, String> {
    if server_url.is_empty() {
        return Err("invalid_config".into());
    }
    if !path.starts_with('/')
        || path.contains("..")
        || path.contains('\\')
        || path.contains('\0')
        || path.contains('?')
        || path.contains('#')
    {
        return Err("invalid_config".into());
    }
    validate_server_url(server_url)?;
    let base = reqwest::Url::parse(server_url).map_err(|_| "invalid_config".to_string())?;
    let mut url = base.clone();
    url.set_path(path);
    url.set_query(None);
    url.set_fragment(None);
    if url.scheme() != base.scheme()
        || url.host_str() != base.host_str()
        || url.port_or_known_default() != base.port_or_known_default()
        || !url.username().is_empty()
        || url.password().is_some()
    {
        return Err("invalid_config".into());
    }
    if url.path() != path {
        return Err("invalid_config".into());
    }
    Ok(url)
}

fn file_url(server_url: &str, mod_id: &str) -> Result<reqwest::Url, String> {
    api_url(server_url, &format!("/api/mods/{mod_id}/file"))
}

struct TempGuard {
    path: PathBuf,
    keep: bool,
}

impl Drop for TempGuard {
    fn drop(&mut self) {
        if !self.keep {
            let _ = fs::remove_file(&self.path);
        }
    }
}

pub(crate) fn create_temp(dir: &Path) -> Result<PathBuf, String> {
    let mut n = 0u32;
    loop {
        let nanos = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|_| "internal".to_string())?
            .as_nanos();
        let name = format!(".mu-{nanos}-{n}.part");
        if name.ends_with(".jar") {
            return Err("internal".into());
        }
        let path = join_segment(dir, &name)?;
        match OpenOptions::new().write(true).create_new(true).open(&path) {
            Ok(_) => return Ok(path),
            Err(err) if err.kind() == std::io::ErrorKind::AlreadyExists => {
                n += 1;
                if n > 100 {
                    return Err("internal".into());
                }
            }
            Err(_) => return Err("internal".into()),
        }
    }
}

pub(crate) fn download(
    url: &reqwest::Url,
    dest: &Path,
    server_url: &str,
    bearer: Option<&str>,
    progress: &dyn Fn(u64, u64),
) -> Result<String, String> {
    download_within(
        url,
        dest,
        server_url,
        bearer,
        progress,
        Some(Duration::from_secs(120)),
    )
}

pub(crate) fn download_within(
    url: &reqwest::Url,
    dest: &Path,
    server_url: &str,
    bearer: Option<&str>,
    progress: &dyn Fn(u64, u64),
    timeout: Option<Duration>,
) -> Result<String, String> {
    let base = reqwest::Url::parse(server_url).map_err(|_| "invalid_config".to_string())?;
    if url.scheme() != base.scheme()
        || url.host_str() != base.host_str()
        || url.port_or_known_default() != base.port_or_known_default()
    {
        return Err("invalid_config".into());
    }
    if let Some(token) = bearer {
        if token.is_empty() || token.bytes().any(|byte| byte <= 0x20 || byte >= 0x7f) {
            return Err("unauthorized".into());
        }
    }
    let mut builder = reqwest::blocking::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .connect_timeout(Duration::from_secs(10))
        .no_proxy();
    if let Some(limit) = timeout {
        builder = builder.timeout(limit);
    }
    let client = builder.build().map_err(|_| "internal".to_string())?;
    let mut request = client.get(url.clone());
    if let Some(token) = bearer {
        request = request.bearer_auth(token);
    }
    let mut response = request.send().map_err(|_| "internal".to_string())?;
    let status = response.status();
    if status.is_redirection() {
        return Err("internal".into());
    }
    if status.as_u16() == 401 {
        return Err("unauthorized".into());
    }
    if !status.is_success() {
        let text = response.text().unwrap_or_default();
        if let Ok(value) = serde_json::from_str::<serde_json::Value>(&text) {
            if let Some(code) = value.get("error").and_then(|item| item.as_str()) {
                if let Some(text) = api_error_text(code) {
                    return Err(text);
                }
            }
        }
        return Err("internal".into());
    }
    let mut file = OpenOptions::new()
        .write(true)
        .truncate(true)
        .open(dest)
        .map_err(|_| "internal".to_string())?;
    let total = response.content_length().unwrap_or(0);
    let mut loaded = 0u64;
    let mut hasher = Sha256::new();
    let mut buf = [0u8; 64 * 1024];
    loop {
        let n = response
            .read(&mut buf)
            .map_err(|_| "internal".to_string())?;
        if n == 0 {
            break;
        }
        hasher.update(&buf[..n]);
        file.write_all(&buf[..n])
            .map_err(|_| "internal".to_string())?;
        loaded += n as u64;
        progress(loaded, total);
    }
    file.sync_all().map_err(|_| "internal".to_string())?;
    Ok(to_hex(&hasher.finalize()))
}

pub(crate) fn ct_eq(left: &str, right: &str) -> bool {
    let left = left.as_bytes();
    let right = right.as_bytes();
    if left.len() != right.len() {
        return false;
    }
    let mut diff = 0u8;
    for index in 0..left.len() {
        diff |= left[index] ^ right[index];
    }
    diff == 0
}

pub(crate) fn sha256_file(path: &Path) -> Result<String, ()> {
    let mut file = File::open(path).map_err(|_| ())?;
    let meta = file.metadata().map_err(|_| ())?;
    if !meta.is_file() {
        return Err(());
    }
    let mut hasher = Sha256::new();
    let mut buf = [0u8; 64 * 1024];
    loop {
        let n = file.read(&mut buf).map_err(|_| ())?;
        if n == 0 {
            break;
        }
        hasher.update(&buf[..n]);
    }
    Ok(to_hex(&hasher.finalize()))
}

pub(crate) fn to_hex(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut out = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        out.push(HEX[(byte >> 4) as usize] as char);
        out.push(HEX[(byte & 0xf) as usize] as char);
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::BTreeMap;
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::sync::{mpsc, Arc};
    use std::thread;
    use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

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
            let path = std::env::temp_dir().join(format!("modupdater-{label}-{n}-{nanos}"));
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
        super::to_hex(&hasher.finalize())
    }

    fn write_zip(path: &Path, entries: &[(&str, &[u8])]) {
        let file = File::create(path).unwrap();
        let mut zip = zip::ZipWriter::new(file);
        let opts = zip::write::SimpleFileOptions::default();
        for (name, data) in entries {
            zip.start_file(*name, opts).unwrap();
            zip.write_all(data).unwrap();
        }
        zip.finish().unwrap();
    }

    fn names_of(dir: &Path) -> Vec<String> {
        let mut names = fs::read_dir(dir)
            .unwrap()
            .map(|entry| entry.unwrap().file_name().to_string_lossy().into_owned())
            .collect::<Vec<_>>();
        names.sort();
        names
    }

    struct Server {
        port: u16,
        hits: Arc<AtomicUsize>,
        stop: mpsc::Sender<()>,
        handle: Option<thread::JoinHandle<()>>,
    }

    impl Server {
        fn url(&self) -> String {
            format!("http://127.0.0.1:{}", self.port)
        }
    }

    impl Drop for Server {
        fn drop(&mut self) {
            let _ = self.stop.send(());
            if let Some(handle) = self.handle.take() {
                let _ = handle.join();
            }
        }
    }

    fn serve(head: String, body: Vec<u8>) -> Server {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = listener.local_addr().unwrap().port();
        listener.set_nonblocking(true).unwrap();
        let hits = Arc::new(AtomicUsize::new(0));
        let hits_thread = hits.clone();
        let (stop, rx) = mpsc::channel::<()>();
        let handle = thread::spawn(move || {
            let start = Instant::now();
            while start.elapsed() < Duration::from_secs(20) {
                if rx.try_recv().is_ok() {
                    break;
                }
                match listener.accept() {
                    Ok((mut sock, _)) => {
                        hits_thread.fetch_add(1, Ordering::SeqCst);
                        let _ = sock.set_nonblocking(false);
                        let _ = sock.set_read_timeout(Some(Duration::from_secs(2)));
                        let _ = sock.set_write_timeout(Some(Duration::from_secs(2)));
                        read_headers(&mut sock);
                        let _ = sock.write_all(head.as_bytes());
                        let _ = sock.write_all(&body);
                        let _ = sock.flush();
                    }
                    Err(err) if err.kind() == std::io::ErrorKind::WouldBlock => {
                        thread::sleep(Duration::from_millis(10));
                    }
                    Err(_) => break,
                }
            }
        });
        Server {
            port,
            hits,
            stop,
            handle: Some(handle),
        }
    }

    fn read_headers(sock: &mut std::net::TcpStream) {
        let mut buf = Vec::new();
        let mut tmp = [0u8; 1024];
        let start = Instant::now();
        while start.elapsed() < Duration::from_secs(2) {
            match sock.read(&mut tmp) {
                Ok(0) => return,
                Ok(n) => {
                    buf.extend_from_slice(&tmp[..n]);
                    if buf.windows(4).any(|window| window == b"\r\n\r\n") {
                        return;
                    }
                }
                Err(err)
                    if err.kind() == std::io::ErrorKind::WouldBlock
                        || err.kind() == std::io::ErrorKind::TimedOut =>
                {
                    thread::sleep(Duration::from_millis(5));
                }
                Err(_) => return,
            }
        }
    }

    fn http_ok(body: &[u8]) -> (String, Vec<u8>) {
        (
            format!(
                "HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                body.len()
            ),
            body.to_vec(),
        )
    }

    #[test]
    fn safe_jar_name_rules() {
        assert_eq!(safe_jar_name("Sodium.jar").unwrap(), "Sodium.jar");
        assert_eq!(safe_jar_name("foo/bar.jar").unwrap(), "bar.jar");
        assert_eq!(safe_jar_name("C:\\mods\\bar.jar").unwrap(), "bar.jar");
        assert_eq!(safe_jar_name("My Mod.jar").unwrap(), "My Mod.jar");
        assert_eq!(safe_jar_name("(api).jar").unwrap(), "(api).jar");
        assert!(safe_jar_name("../bar.jar").is_err());
        assert!(safe_jar_name("foo/../bar.jar").is_err());
        assert!(safe_jar_name("a\".jar").is_err());
        assert!(safe_jar_name("a\n.jar").is_err());
        assert!(safe_jar_name("a\r.jar").is_err());
        assert!(safe_jar_name("a\0.jar").is_err());
        assert!(safe_jar_name("foo..jar").is_err());
        assert!(safe_jar_name(".jar").is_err());
        assert!(safe_jar_name("a.jar").is_ok());
        assert!(safe_jar_name("-a.jar").is_err());
        assert!(safe_jar_name("a.txt").is_err());
        let long = format!("{}{}.jar", "a", "b".repeat(175));
        assert_eq!(long.len(), 180);
        assert!(safe_jar_name(&long).is_ok());
        let longer = format!("{}{}.jar", "a", "b".repeat(176));
        assert_eq!(longer.len(), 181);
        assert!(safe_jar_name(&longer).is_err());
    }

    #[test]
    fn scan_skips_symlinks_directories_and_unsafe_names() {
        let tmp = Tmp::new("scan");
        let mods = tmp.path.join("mods");
        fs::create_dir(&mods).unwrap();
        write_zip(
            &mods.join("Sodium.jar"),
            &[(
                "fabric.mod.json",
                br#"{"id":" sodium ","version":"0.6.0","description":["a","b"]}"#,
            )],
        );
        fs::write(mods.join("plain.jar"), b"hello-jar").unwrap();
        write_zip(&mods.join("broken.jar"), &[("fabric.mod.json", b"{")]);
        write_zip(
            &mods.join("QuiltMod.jar"),
            &[("quilt.mod.json", br#"{"id":"quiltmod","version":"2.0.0"}"#)],
        );
        let toml = b"modLoader=\"javafml\"\n[[mods]]\nmodId=\"forgemod\"\nversion=\"1.2.3\"\ndisplayName=\"Forge Mod\"\ndescription=\"Hello\"\n[[dependencies.forgemod]]\nmodId=\"forge\"\n";
        write_zip(
            &mods.join("ForgeMod.jar"),
            &[("META-INF/mods.toml", toml.as_slice())],
        );
        write_zip(
            &mods.join("Both.jar"),
            &[
                ("fabric.mod.json", br#"{"id":"from-fabric","version":"9"}"#),
                (
                    "META-INF/mods.toml",
                    b"[[mods]]\nmodId=\"from-toml\"\nversion=\"1\"\n",
                ),
            ],
        );
        let long = "v".repeat(70);
        let long_json = format!("{{\"id\":\"longver\",\"version\":\"{long}\"}}");
        write_zip(
            &mods.join("LongVer.jar"),
            &[("fabric.mod.json", long_json.as_bytes())],
        );
        write_zip(
            &mods.join("badslug.jar"),
            &[("fabric.mod.json", br#"{"id":"bad/id","version":"1"}"#)],
        );
        fs::write(mods.join("zero.jar"), b"").unwrap();
        fs::write(mods.join("-bad.jar"), b"nope").unwrap();
        fs::write(mods.join("foo..jar"), b"nope").unwrap();
        fs::write(mods.join("notes.txt"), b"nope").unwrap();
        fs::create_dir(mods.join("dir.jar")).unwrap();
        fs::write(mods.join("dir.jar").join("inner.jar"), b"nope").unwrap();
        fs::create_dir(mods.join("nested")).unwrap();
        fs::write(mods.join("nested").join("hidden.jar"), b"nope").unwrap();
        #[cfg(unix)]
        std::os::unix::fs::symlink(mods.join("Sodium.jar"), mods.join("link.jar")).unwrap();

        let before = names_of(&mods);
        let found = scan_dir(&mods).unwrap();
        let after = names_of(&mods);
        assert_eq!(before, after);

        let map: BTreeMap<_, _> = found
            .iter()
            .map(|jar| (jar.filename.as_str(), jar))
            .collect();
        assert_eq!(
            map.keys().copied().collect::<Vec<_>>(),
            vec![
                "Both.jar",
                "ForgeMod.jar",
                "LongVer.jar",
                "QuiltMod.jar",
                "Sodium.jar",
                "broken.jar",
                "plain.jar",
            ]
        );
        assert_eq!(map["Sodium.jar"].slug, "sodium");
        assert_eq!(map["Sodium.jar"].version, "0.6.0");
        assert_eq!(map["plain.jar"].slug, "plain");
        assert_eq!(map["plain.jar"].version, "");
        assert_eq!(map["plain.jar"].sha256, digest(b"hello-jar"));
        assert_eq!(map["plain.jar"].size, 9);
        assert_eq!(map["broken.jar"].slug, "broken");
        assert_eq!(map["broken.jar"].version, "");
        assert_eq!(map["QuiltMod.jar"].slug, "quiltmod");
        assert_eq!(map["QuiltMod.jar"].version, "2.0.0");
        assert_eq!(map["ForgeMod.jar"].slug, "forgemod");
        assert_eq!(map["ForgeMod.jar"].version, "1.2.3");
        assert_eq!(map["Both.jar"].slug, "from-fabric");
        assert_eq!(map["Both.jar"].version, "9");
        assert_eq!(map["LongVer.jar"].version, "v".repeat(64));
        assert!(map.get("badslug.jar").is_none());
        assert!(map.get("link.jar").is_none());
        assert!(map.get("hidden.jar").is_none());
        assert!(map.get("zero.jar").is_none());
        assert!(map.get("dir.jar").is_none());
    }

    #[test]
    fn temp_file_is_inside_mods_dir_and_not_jar() {
        let tmp = Tmp::new("temp");
        let dir = tmp.path.canonicalize().unwrap();
        let path = create_temp(&dir).unwrap();
        assert_eq!(path.parent().unwrap(), dir.as_path());
        let name = path.file_name().unwrap().to_str().unwrap();
        assert!(!name.ends_with(".jar"));
        assert!(name.ends_with(".part"));
        let _ = fs::remove_file(path);
    }

    #[test]
    fn install_hash_mismatch_deletes_temp_and_keeps_jar() {
        let tmp = Tmp::new("mismatch");
        let mods = tmp.path.join("mods");
        fs::create_dir(&mods).unwrap();
        let mods = mods.canonicalize().unwrap();
        fs::write(mods.join("sodium-0.6.jar"), b"OLD").unwrap();
        let (head, body) = http_ok(b"NEW");
        let server = serve(head, body);
        let err = install_jar(
            &mods,
            &server.url(),
            &"ab".repeat(16),
            "sodium-0.6.jar",
            &digest(b"OTHER"),
            None,
            &|_, _| {},
        )
        .unwrap_err();
        assert_eq!(err, "hash_mismatch");
        assert_eq!(fs::read(mods.join("sodium-0.6.jar")).unwrap(), b"OLD");
        assert!(names_of(&mods)
            .iter()
            .all(|name| !name.ends_with(".part") && !name.starts_with(".mu-")));
        assert_eq!(names_of(&mods), vec!["sodium-0.6.jar".to_string()]);
    }

    #[test]
    fn install_refuses_bad_id_and_slash_filename() {
        let tmp = Tmp::new("refuse");
        let mods = tmp.path.join("mods");
        fs::create_dir(&mods).unwrap();
        let mods = mods.canonicalize().unwrap();
        fs::write(mods.join("sodium-0.6.jar"), b"OLD").unwrap();
        let id_err = install_jar(
            &mods,
            "http://127.0.0.1:9",
            "../abc",
            "sodium-0.6.jar",
            &digest(b"OLD"),
            None,
            &|_, _| {},
        )
        .unwrap_err();
        assert_eq!(id_err, "not_found");
        let name_err = install_jar(
            &mods,
            "http://127.0.0.1:9",
            &"ab".repeat(16),
            "dir/sodium-0.6.jar",
            &digest(b"OLD"),
            None,
            &|_, _| {},
        )
        .unwrap_err();
        assert_eq!(name_err, "invalid_filename");
        assert_eq!(fs::read(mods.join("sodium-0.6.jar")).unwrap(), b"OLD");
        assert_eq!(names_of(&mods), vec!["sodium-0.6.jar".to_string()]);
    }

    #[test]
    fn install_does_not_follow_redirect() {
        let tmp = Tmp::new("redirect");
        let mods = tmp.path.join("mods");
        fs::create_dir(&mods).unwrap();
        let mods = mods.canonicalize().unwrap();
        fs::write(mods.join("sodium-0.6.jar"), b"OLD").unwrap();
        let payload = b"NEW";
        let (ok_head, ok_body) = http_ok(payload);
        let decoy = serve(ok_head, ok_body);
        let redirect = format!(
            "HTTP/1.1 302 Found\r\nLocation: {}/api/mods/{}/file\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
            decoy.url(),
            "cd".repeat(16)
        );
        let server = serve(redirect, Vec::new());
        let err = install_jar(
            &mods,
            &server.url(),
            &"ab".repeat(16),
            "sodium-0.6.jar",
            &digest(payload),
            None,
            &|_, _| {},
        )
        .unwrap_err();
        assert_eq!(err, "internal");
        assert_eq!(decoy.hits.load(Ordering::SeqCst), 0);
        assert_eq!(fs::read(mods.join("sodium-0.6.jar")).unwrap(), b"OLD");
        assert_eq!(names_of(&mods), vec!["sodium-0.6.jar".to_string()]);
    }

    #[test]
    fn install_replaces_jar_and_writes_bak() {
        let tmp = Tmp::new("install");
        let mods = tmp.path.join("mods");
        fs::create_dir(&mods).unwrap();
        let mods = mods.canonicalize().unwrap();
        fs::write(mods.join("sodium-0.6.jar"), b"OLD").unwrap();
        fs::write(mods.join("other.jar"), b"OTHER").unwrap();
        let (head, body) = http_ok(b"NEW");
        let server = serve(head, body);
        install_jar(
            &mods,
            &server.url(),
            &"ab".repeat(16),
            "sodium-0.6.jar",
            &digest(b"NEW"),
            Some("other.jar"),
            &|_, _| {},
        )
        .unwrap();
        assert_eq!(fs::read(mods.join("sodium-0.6.jar")).unwrap(), b"NEW");
        assert!(!mods.join("other.jar").exists());
        let names = names_of(&mods);
        assert!(names.iter().any(|name| name == "sodium-0.6.jar"));
        let bak = names
            .iter()
            .find(|name| name.starts_with("other.jar."))
            .unwrap();
        assert!(bak.ends_with(".bak"));
        assert_eq!(mods.join(bak).parent().unwrap(), mods.as_path());
        assert_eq!(fs::read(mods.join(bak)).unwrap(), b"OTHER");
        assert!(names.iter().any(|name| is_unix_bak(name, "sodium-0.6")));
        assert!(names.iter().all(|name| !name.ends_with(".part")));
    }

    #[test]
    fn bak_of_sodium_stays_inside_mods_dir() {
        let tmp = Tmp::new("bak");
        let mods = tmp.path.join("mods");
        fs::create_dir(&mods).unwrap();
        let mods = mods.canonicalize().unwrap();
        fs::write(mods.join("sodium-0.6.jar"), b"DATA").unwrap();
        backup_jar(&mods, "sodium-0.6.jar").unwrap();
        assert!(!mods.join("sodium-0.6.jar").exists());
        let names = names_of(&mods);
        assert_eq!(names.len(), 1);
        assert!(is_unix_bak(&names[0], "sodium-0.6"), "{}", names[0]);
        assert_eq!(mods.join(&names[0]).parent().unwrap(), mods.as_path());
        assert_eq!(fs::read(mods.join(&names[0])).unwrap(), b"DATA");
    }

    #[test]
    fn bak_name_increments_when_taken() {
        let tmp = Tmp::new("bakn");
        let dir = tmp.path.join("mods");
        fs::create_dir(&dir).unwrap();
        let dir = dir.canonicalize().unwrap();
        let first = next_bak_name(&dir, "sodium-0.6", 100).unwrap();
        assert_eq!(first, "sodium-0.6.jar.100.bak");
        fs::write(dir.join(&first), b"x").unwrap();
        let second = next_bak_name(&dir, "sodium-0.6", 100).unwrap();
        assert_eq!(second, "sodium-0.6.jar.100-2.bak");
        fs::write(dir.join(&second), b"y").unwrap();
        let third = next_bak_name(&dir, "sodium-0.6", 100).unwrap();
        assert_eq!(third, "sodium-0.6.jar.100-3.bak");
    }

    fn is_unix_bak(name: &str, stem: &str) -> bool {
        let Some(rest) = name.strip_prefix(&format!("{stem}.jar.")) else {
            return false;
        };
        let Some(rest) = rest.strip_suffix(".bak") else {
            return false;
        };
        !rest.is_empty() && rest.bytes().all(|byte| byte.is_ascii_digit())
    }
}
