use std::fs;
use std::path::PathBuf;

fn main() {
    let url = server_url();
    println!("cargo:rustc-env=MODUPDATER_SERVER_URL={url}");
    println!("cargo:rerun-if-env-changed=MODUPDATER_SERVER_URL");
    let manifest = PathBuf::from(std::env::var("CARGO_MANIFEST_DIR").unwrap());
    println!(
        "cargo:rerun-if-changed={}",
        manifest.join("../../.env").display()
    );
    tauri_build::build();
}

fn server_url() -> String {
    if let Some(url) = clean(&std::env::var("MODUPDATER_SERVER_URL").unwrap_or_default()) {
        return url;
    }
    let manifest = PathBuf::from(std::env::var("CARGO_MANIFEST_DIR").unwrap());
    let env_file = manifest.join("../../.env");
    if let Ok(text) = fs::read_to_string(&env_file) {
        for line in text.lines() {
            let line = line.trim();
            if line.is_empty() || line.starts_with('#') {
                continue;
            }
            let Some(value) = line.strip_prefix("MODUPDATER_SERVER_URL=") else {
                continue;
            };
            let value = value.trim().trim_matches('"').trim_matches('\'');
            if let Some(url) = clean(value) {
                return url;
            }
            panic!(
                "MODUPDATER_SERVER_URL in .env must be http://host[:port] or https://host[:port]"
            );
        }
    }
    "http://127.0.0.1:8080".to_string()
}

fn clean(raw: &str) -> Option<String> {
    let value = raw.trim().trim_end_matches('/').to_string();
    if value.is_empty() {
        return None;
    }
    let rest = value
        .strip_prefix("https://")
        .or_else(|| value.strip_prefix("http://"))?;
    if rest.is_empty()
        || rest
            .chars()
            .any(|c| !(c.is_ascii_alphanumeric() || matches!(c, '.' | '-' | '_' | ':')))
    {
        return None;
    }
    Some(value)
}
