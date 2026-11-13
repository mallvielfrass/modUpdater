#!/usr/bin/env python3
import json
import os
import sys
import urllib.error
import urllib.request
import uuid
from pathlib import Path

SLUG = "modupdater"
NAME = "Mod Updater"


def default_server():
    raw = os.environ.get("MODUPDATER_SERVER_URL", "").strip().rstrip("/")
    if raw:
        return raw
    env_file = Path(__file__).resolve().parents[1] / ".env"
    if env_file.is_file():
        for line in env_file.read_text(encoding="utf-8").splitlines():
            text = line.strip()
            if not text or text.startswith("#") or not text.startswith("MODUPDATER_SERVER_URL="):
                continue
            value = text.split("=", 1)[1].strip().strip('"').strip("'").rstrip("/")
            if value:
                return value
    return "http://127.0.0.1:8080"


def refresh_path():
    return Path.home() / ".config" / "modupdater" / "upload-refresh"


def request(url, data=None, headers=None, method=None):
    req = urllib.request.Request(url, data=data, method=method)
    for key, value in (headers or {}).items():
        req.add_header(key, value)
    try:
        with urllib.request.urlopen(req, timeout=600) as resp:
            body = resp.read()
            return resp.status, body
    except urllib.error.HTTPError as err:
        return err.code, err.read()


def save_refresh(token):
    path = refresh_path()
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(path.parent, 0o700)
    tmp = path.parent / f".upload-refresh-{uuid.uuid4().hex}"
    tmp.write_text(token + "\n", encoding="utf-8")
    os.chmod(tmp, 0o600)
    tmp.replace(path)
    os.chmod(path, 0o600)


def acquire_token(server):
    env = os.environ.get("MODUPDATER_TOKEN", "").strip()
    if env:
        return env
    path = refresh_path()
    if not path.is_file():
        print("нет входа, сначала make upload", file=sys.stderr)
        return None
    refresh = path.read_text(encoding="utf-8").strip()
    if not refresh:
        print("нет входа, сначала make upload", file=sys.stderr)
        return None
    payload = json.dumps({"refresh_token": refresh}).encode()
    status, body = request(
        server + "/api/auth/refresh",
        payload,
        {"Content-Type": "application/json"},
        "POST",
    )
    if status // 100 != 2:
        print("нет входа, сначала make upload", file=sys.stderr)
        return None
    parsed = json.loads(body.decode() or "{}")
    token = parsed.get("token") or ""
    nxt = parsed.get("refresh_token") or ""
    if not token or not nxt:
        print("нет входа, сначала make upload", file=sys.stderr)
        return None
    save_refresh(nxt)
    return token


def auth_headers(token, content_type=None):
    headers = {"Authorization": "Bearer " + token}
    if content_type:
        headers["Content-Type"] = content_type
    return headers


def find_mod(server, token):
    status, body = request(server + "/api/admin/mods", headers=auth_headers(token), method="GET")
    if status // 100 != 2:
        raise SystemExit("не удалось прочитать список модов")
    parsed = json.loads(body.decode() or "{}")
    for mod in parsed.get("mods") or []:
        if mod.get("slug") == SLUG and not mod.get("revoked"):
            return mod.get("id") or ""
    return ""


def multipart(filename, data, fields):
    boundary = "----modupdater" + uuid.uuid4().hex
    chunks = []
    for key, value in fields.items():
        chunks.append(f"--{boundary}\r\nContent-Disposition: form-data; name=\"{key}\"\r\n\r\n{value}\r\n".encode())
    head = (
        f"--{boundary}\r\n"
        f"Content-Disposition: form-data; name=\"file\"; filename=\"{filename}\"\r\n"
        f"Content-Type: application/java-archive\r\n\r\n"
    ).encode()
    chunks.append(head + data + b"\r\n")
    chunks.append(f"--{boundary}--\r\n".encode())
    return b"".join(chunks), "multipart/form-data; boundary=" + boundary


def main():
    server = default_server()
    jar = ""
    args = sys.argv[1:]
    i = 0
    while i < len(args):
        if args[i] == "--server" and i + 1 < len(args):
            server = args[i + 1].rstrip("/")
            i += 2
        elif args[i] == "--jar" and i + 1 < len(args):
            jar = args[i + 1]
            i += 2
        else:
            print("usage: upload-forge.py --server URL --jar FILE", file=sys.stderr)
            return 2
    path = Path(jar)
    if not path.is_file():
        print(f"нет файла {path}", file=sys.stderr)
        return 1
    token = acquire_token(server)
    if not token:
        return 1
    existing = find_mod(server, token)
    body, ctype = multipart(path.name, path.read_bytes(), {"name": NAME, "client_only": "true", "client_required": "true"})
    if existing:
        url = f"{server}/api/admin/mods/{existing}"
        method = "PUT"
    else:
        url = server + "/api/admin/mods"
        method = "POST"
    status, raw = request(url, body, auth_headers(token, ctype), method)
    if status // 100 != 2:
        print("не удалось загрузить мод", file=sys.stderr)
        return 1
    print("загружен", path.name)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
