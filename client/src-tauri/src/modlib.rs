use std::fs::{self, File};
use std::io::Read;
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

use zip::ZipArchive;

const META_LIMIT: u64 = 1024 * 1024;

pub fn safe_jar_name(raw: &str) -> Result<String, String> {
    if raw
        .bytes()
        .any(|b| b == 0 || b == b'\r' || b == b'\n' || b == b'"')
    {
        return Err("invalid_filename".into());
    }
    let segments: Vec<&str> = raw.split(['/', '\\']).collect();
    if segments.iter().any(|segment| *segment == "..") {
        return Err("invalid_filename".into());
    }
    let last = segments.last().copied().unwrap_or("");
    if !jar_name_ok(last) {
        return Err("invalid_filename".into());
    }
    Ok(last.to_string())
}

fn jar_name_ok(name: &str) -> bool {
    let bytes = name.as_bytes();
    if bytes.len() < 5 || bytes.len() > 180 || name.contains("..") || !bytes.ends_with(b".jar") {
        return false;
    }
    let head = &bytes[..bytes.len() - 4];
    if head.is_empty() || !is_first(head[0]) {
        return false;
    }
    head[1..].iter().copied().all(is_rest)
}

fn is_first(c: u8) -> bool {
    c.is_ascii_alphanumeric() || c == b'(' || c == b'['
}

fn is_rest(c: u8) -> bool {
    c.is_ascii_alphanumeric()
        || matches!(
            c,
            b'.' | b'_' | b'+' | b'(' | b')' | b'[' | b']' | b' ' | b'-'
        )
}

pub(crate) fn valid_mod_id(id: &str) -> bool {
    let bytes = id.as_bytes();
    bytes.len() == 32 && bytes.iter().copied().all(is_lower_hex)
}

fn is_lower_hex(byte: u8) -> bool {
    byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte)
}

pub(crate) fn rename_to_bak(dir: &Path, filename: &str) -> Result<(), String> {
    let src = join_segment(dir, filename)?;
    let meta = fs::symlink_metadata(&src).map_err(|_| "not_found".to_string())?;
    if !meta.file_type().is_file() {
        return Err("invalid_filename".into());
    }
    let stem = filename.strip_suffix(".jar").ok_or("invalid_filename")?;
    let unix = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| "internal".to_string())?
        .as_secs();
    let bak = next_bak_name(dir, stem, unix)?;
    let dest = join_segment(dir, &bak)?;
    fs::rename(&src, &dest).map_err(|_| "internal".to_string())?;
    Ok(())
}

pub(crate) fn next_bak_name(dir: &Path, stem: &str, unix: u64) -> Result<String, String> {
    let primary = format!("{stem}.jar.{unix}.bak");
    if !entry_exists(&join_segment(dir, &primary)?) {
        return Ok(primary);
    }
    let mut n = 2u32;
    loop {
        let name = format!("{stem}.jar.{unix}-{n}.bak");
        if name.len() > 255 {
            return Err("invalid_filename".into());
        }
        if !entry_exists(&join_segment(dir, &name)?) {
            return Ok(name);
        }
        n += 1;
        if n > 1000 {
            return Err("internal".into());
        }
    }
}

fn entry_exists(path: &Path) -> bool {
    fs::symlink_metadata(path).is_ok()
}

pub(crate) fn join_segment(dir: &Path, name: &str) -> Result<PathBuf, String> {
    if name.is_empty()
        || name == "."
        || name == ".."
        || name.contains('/')
        || name.contains('\\')
        || name.contains('\0')
    {
        return Err("invalid_filename".into());
    }
    let path = dir.join(name);
    match path.parent() {
        Some(parent) if parent == dir => Ok(path),
        _ => Err("invalid_filename".into()),
    }
}

pub(crate) enum ReadMeta {
    Skip,
}

struct ParsedMeta {
    id: Option<String>,
    version: String,
}

pub(crate) fn read_slug_version(path: &Path, stem: &str) -> Result<(String, String), ReadMeta> {
    let file = File::open(path).map_err(|_| ReadMeta::Skip)?;
    let mut archive = match ZipArchive::new(file) {
        Ok(archive) => archive,
        Err(_) => return finish_slug(None, "", stem),
    };
    const NAMES: [&str; 3] = ["fabric.mod.json", "quilt.mod.json", "META-INF/mods.toml"];
    for name in NAMES {
        match read_entry(&mut archive, name) {
            Ok(None) => continue,
            Ok(Some(buf)) => {
                let parsed = if name.ends_with(".toml") {
                    parse_toml_meta(&buf)
                } else {
                    parse_json_meta(&buf)
                };
                return match parsed {
                    Ok(meta) => finish_slug(meta.id, &meta.version, stem),
                    Err(()) => finish_slug(None, "", stem),
                };
            }
            Err(()) => return finish_slug(None, "", stem),
        }
    }
    finish_slug(None, "", stem)
}

fn read_entry(archive: &mut ZipArchive<File>, name: &str) -> Result<Option<Vec<u8>>, ()> {
    let mut entry = match archive.by_name(name) {
        Ok(entry) => entry,
        Err(zip::result::ZipError::FileNotFound) => return Ok(None),
        Err(_) => return Err(()),
    };
    let mut buf = Vec::new();
    entry
        .by_ref()
        .take(META_LIMIT)
        .read_to_end(&mut buf)
        .map_err(|_| ())?;
    Ok(Some(buf))
}

fn finish_slug(
    id: Option<String>,
    version: &str,
    stem: &str,
) -> Result<(String, String), ReadMeta> {
    let slug = match id {
        Some(value) if !value.is_empty() => value,
        _ => stem.to_string(),
    };
    if !valid_slug(&slug) {
        return Err(ReadMeta::Skip);
    }
    Ok((slug, truncate_bytes(version, 64)))
}

fn valid_slug(slug: &str) -> bool {
    let bytes = slug.as_bytes();
    (1..=128).contains(&bytes.len())
        && !bytes
            .iter()
            .any(|byte| matches!(byte, b'/' | b'\\' | 0 | b'\r' | b'\n'))
}

fn truncate_bytes(value: &str, max: usize) -> String {
    if value.len() <= max {
        return value.to_string();
    }
    let mut end = max;
    while end > 0 && !value.is_char_boundary(end) {
        end -= 1;
    }
    value[..end].to_string()
}

fn parse_json_meta(bytes: &[u8]) -> Result<ParsedMeta, ()> {
    let value: serde_json::Value = serde_json::from_slice(bytes).map_err(|_| ())?;
    let id = value
        .get("id")
        .and_then(|item| item.as_str())
        .map(str::trim)
        .filter(|item| !item.is_empty())
        .map(str::to_string);
    let version = value
        .get("version")
        .and_then(|item| item.as_str())
        .unwrap_or("");
    Ok(ParsedMeta {
        id,
        version: truncate_bytes(version, 64),
    })
}

fn parse_toml_meta(bytes: &[u8]) -> Result<ParsedMeta, ()> {
    let text = std::str::from_utf8(bytes).map_err(|_| ())?;
    let start = find_mods_table(text).ok_or(())?;
    let after = &text[start..];
    let body = match after.find("[[") {
        Some(index) => &after[..index],
        None => after,
    };
    let mut id = None;
    let mut version = String::new();
    for (key, value) in toml_pairs(body) {
        if key == "modId" && id.is_none() {
            let trimmed = value.trim();
            if !trimmed.is_empty() {
                id = Some(trimmed.to_string());
            }
        } else if key == "version" && version.is_empty() {
            version = truncate_bytes(&value, 64);
        }
    }
    Ok(ParsedMeta { id, version })
}

fn find_mods_table(text: &str) -> Option<usize> {
    let mut offset = 0;
    for line in text.split_inclusive('\n') {
        let head = line.trim().split('#').next().unwrap_or("").trim();
        if head == "[[mods]]" {
            return Some(offset + line.len());
        }
        offset += line.len();
    }
    None
}

fn toml_pairs(body: &str) -> Vec<(String, String)> {
    let chars: Vec<char> = body.chars().collect();
    let mut out = Vec::new();
    let mut index = 0;
    while index < chars.len() {
        while index < chars.len() && chars[index].is_whitespace() {
            index += 1;
        }
        if index >= chars.len() {
            break;
        }
        if chars[index] == '#' {
            while index < chars.len() && chars[index] != '\n' {
                index += 1;
            }
            continue;
        }
        if !(chars[index].is_ascii_alphanumeric() || chars[index] == '_') {
            index += 1;
            continue;
        }
        let key_start = index;
        index += 1;
        while index < chars.len()
            && (chars[index].is_ascii_alphanumeric() || chars[index] == '_' || chars[index] == '-')
        {
            index += 1;
        }
        let key: String = chars[key_start..index].iter().collect();
        while index < chars.len() && (chars[index] == ' ' || chars[index] == '\t') {
            index += 1;
        }
        if index >= chars.len() || chars[index] != '=' {
            continue;
        }
        index += 1;
        while index < chars.len() && (chars[index] == ' ' || chars[index] == '\t') {
            index += 1;
        }
        if index >= chars.len() {
            break;
        }
        let Some((value, next)) = read_toml_value(&chars, index) else {
            break;
        };
        out.push((key, value));
        index = next;
    }
    out
}

fn read_toml_value(chars: &[char], index: usize) -> Option<(String, usize)> {
    if starts_with_quotes(chars, index, '"', 3) {
        return read_multiline(chars, index + 3, '"', true);
    }
    if starts_with_quotes(chars, index, '\'', 3) {
        return read_multiline(chars, index + 3, '\'', false);
    }
    if chars[index] == '"' {
        return read_quoted(chars, index + 1, '"', true);
    }
    if chars[index] == '\'' {
        return read_quoted(chars, index + 1, '\'', false);
    }
    let start = index;
    let mut end = index;
    while end < chars.len() && chars[end] != '\n' && chars[end] != '#' && chars[end] != '\r' {
        end += 1;
    }
    let value: String = chars[start..end].iter().collect();
    Some((value.trim().to_string(), end))
}

fn starts_with_quotes(chars: &[char], index: usize, quote: char, count: usize) -> bool {
    index + count <= chars.len()
        && chars[index..index + count]
            .iter()
            .all(|item| *item == quote)
}

fn read_quoted(
    chars: &[char],
    mut index: usize,
    quote: char,
    escapes: bool,
) -> Option<(String, usize)> {
    let mut out = String::new();
    while index < chars.len() {
        let ch = chars[index];
        if ch == quote {
            return Some((out, index + 1));
        }
        if ch == '\n' {
            return None;
        }
        if escapes && ch == '\\' {
            index += 1;
            if index >= chars.len() {
                return None;
            }
            out.push(unescape(chars[index]));
            index += 1;
            continue;
        }
        out.push(ch);
        index += 1;
    }
    None
}

fn read_multiline(
    chars: &[char],
    mut index: usize,
    quote: char,
    escapes: bool,
) -> Option<(String, usize)> {
    let mut out = String::new();
    if index < chars.len() && chars[index] == '\n' {
        index += 1;
    } else if index + 1 < chars.len() && chars[index] == '\r' && chars[index + 1] == '\n' {
        index += 2;
    }
    while index < chars.len() {
        if starts_with_quotes(chars, index, quote, 3) {
            return Some((out, index + 3));
        }
        if escapes && chars[index] == '\\' {
            index += 1;
            if index >= chars.len() {
                return None;
            }
            if chars[index] == '\n' {
                index += 1;
                continue;
            }
            if chars[index] == '\r' {
                index += 1;
                if index < chars.len() && chars[index] == '\n' {
                    index += 1;
                }
                continue;
            }
            out.push(unescape(chars[index]));
            index += 1;
            continue;
        }
        out.push(chars[index]);
        index += 1;
    }
    None
}

fn unescape(ch: char) -> char {
    match ch {
        'n' => '\n',
        'r' => '\r',
        't' => '\t',
        '\\' => '\\',
        '"' => '"',
        other => other,
    }
}
