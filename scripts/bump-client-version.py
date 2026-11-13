#!/usr/bin/env python3
import re
import sys
from pathlib import Path

VER = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")


def die(msg):
    print(msg, file=sys.stderr)
    raise SystemExit(1)


def read_text(path):
    with path.open(newline="") as f:
        return f.read()


def write_text(path, text):
    with path.open("w", newline="") as f:
        f.write(text)


def split_nl(line):
    if line.endswith("\r\n"):
        return line[:-2], "\r\n"
    if line.endswith("\n"):
        return line[:-1], "\n"
    return line, ""


def json_version(text):
    m = re.search(r'^[ \t]*"version"[ \t]*:[ \t]*"([^"]*)"', text, re.M)
    if not m or not VER.fullmatch(m.group(1)):
        die("версия не X.Y.Z")
    return m.group(1)


def set_json_version(text, new):
    new_text, n = re.subn(
        r'^([ \t]*"version"[ \t]*:[ \t]*")[^"]*(")',
        lambda m: m.group(1) + new + m.group(2),
        text,
        count=1,
        flags=re.M,
    )
    if n != 1:
        die("версия не X.Y.Z")
    return new_text


def package_version_line(text):
    in_pkg = False
    for line in text.splitlines(keepends=True):
        body, _nl = split_nl(line)
        if re.fullmatch(r"\[package\][ \t]*", body):
            in_pkg = True
            continue
        if in_pkg and body.startswith("["):
            break
        if in_pkg:
            m = re.fullmatch(r'version[ \t]*=[ \t]*"([^"]*)"[ \t]*(?:#.*)?', body)
            if m:
                return m.group(1)
    return None


def cargo_package_version(text):
    ver = package_version_line(text)
    if ver is None or not VER.fullmatch(ver):
        die("версия не X.Y.Z")
    return ver


def set_cargo_package_version(text, new):
    lines = text.splitlines(keepends=True)
    in_pkg = False
    for i, line in enumerate(lines):
        body, nl = split_nl(line)
        if re.fullmatch(r"\[package\][ \t]*", body):
            in_pkg = True
            continue
        if in_pkg and body.startswith("["):
            break
        if in_pkg:
            m = re.fullmatch(r'(version[ \t]*=[ \t]*")([^"]*)(")([ \t]*(?:#.*)?)', body)
            if m:
                lines[i] = m.group(1) + new + m.group(3) + m.group(4) + nl
                return "".join(lines)
    die("версия не X.Y.Z")


def set_lock(text, new):
    lines = text.splitlines(keepends=True)
    out = []
    i = 0
    n = len(lines)
    while i < n:
        body, _nl = split_nl(lines[i])
        if body.strip() != "[[package]]":
            out.append(lines[i])
            i += 1
            continue
        block = [lines[i]]
        i += 1
        while i < n and split_nl(lines[i])[0].strip() != "[[package]]":
            block.append(lines[i])
            i += 1
        name = None
        ver_idx = None
        for j, line in enumerate(block):
            b, _nl = split_nl(line)
            m = re.fullmatch(r'name[ \t]*=[ \t]*"([^"]*)"[ \t]*', b)
            if m and name is None:
                name = m.group(1)
            if ver_idx is None and re.match(r'version[ \t]*=[ \t]*"', b):
                ver_idx = j
        if name == "modupdater-client" and ver_idx is not None:
            b, nl = split_nl(block[ver_idx])
            m = re.fullmatch(r'(version[ \t]*=[ \t]*")([^"]*)(".*)', b)
            if not m:
                die("версия не X.Y.Z")
            block[ver_idx] = m.group(1) + new + m.group(3) + nl
        out.extend(block)
    return "".join(out)


def bump_patch(ver):
    major, minor, patch = ver.split(".")
    return "%s.%s.%d" % (major, minor, int(patch) + 1)


def main():
    if len(sys.argv) > 2:
        die("версия не X.Y.Z")
    if len(sys.argv) == 2:
        root = Path(sys.argv[1])
    else:
        root = Path(__file__).resolve().parent.parent / "client"
    pkg_p = root / "package.json"
    tauri_p = root / "src-tauri" / "tauri.conf.json"
    cargo_p = root / "src-tauri" / "Cargo.toml"
    lock_p = root / "src-tauri" / "Cargo.lock"

    pkg = read_text(pkg_p)
    tauri = read_text(tauri_p)
    cargo = read_text(cargo_p)
    current = json_version(pkg)
    if json_version(tauri) != current or cargo_package_version(cargo) != current:
        die("версии не совпадают")
    new = bump_patch(current)

    pkg_n = set_json_version(pkg, new)
    tauri_n = set_json_version(tauri, new)
    cargo_n = set_cargo_package_version(cargo, new)
    lock = read_text(lock_p) if lock_p.is_file() else None
    lock_n = set_lock(lock, new) if lock is not None else None

    write_text(pkg_p, pkg_n)
    write_text(tauri_p, tauri_n)
    write_text(cargo_p, cargo_n)
    if lock_n is not None and lock_n != lock:
        write_text(lock_p, lock_n)
    print("%s -> %s" % (current, new))


if __name__ == "__main__":
    main()
