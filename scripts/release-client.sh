#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: ./scripts/release-client.sh [X.Y.Z]" >&2
}

if [[ $# -gt 1 ]]; then
  usage
  exit 1
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLIENT="$ROOT/client"

read_dotenv() {
  local key="$1" file="$ROOT/.env" line val
  if [[ -n "${!key:-}" ]]; then
    printf '%s' "${!key}"
    return
  fi
  [[ -f "$file" ]] || return 0
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line#"${line%%[![:space:]]*}"}"
    [[ -z "$line" || "$line" == \#* ]] && continue
    if [[ "$line" == "$key="* ]]; then
      val="${line#"$key="}"
      val="${val#"${val%%[![:space:]]*}"}"
      val="${val%"${val##*[![:space:]]}"}"
      val="${val%\"}"; val="${val#\"}"
      val="${val%\'}"; val="${val#\'}"
      printf '%s' "$val"
      return
    fi
  done <"$file"
}

SERVER_URL="$(read_dotenv MODUPDATER_SERVER_URL)"
SERVER_URL="${SERVER_URL%/}"
SERVER_URL="${SERVER_URL:-http://127.0.0.1:8080}"
PKG="$CLIENT/package.json"
CARGO="$CLIENT/src-tauri/Cargo.toml"
TAURI="$CLIENT/src-tauri/tauri.conf.json"

missing=0
if ! command -v cargo-xwin >/dev/null 2>&1; then
  echo "cargo-xwin not found." >&2
  echo "Install: cargo install cargo-xwin" >&2
  missing=1
fi
if ! command -v rustup >/dev/null 2>&1 || ! rustup target list --installed | grep -qx 'x86_64-pc-windows-msvc'; then
  echo "rustup target x86_64-pc-windows-msvc is not installed." >&2
  echo "Install: rustup target add x86_64-pc-windows-msvc" >&2
  missing=1
fi
if ! command -v lld >/dev/null 2>&1 && ! command -v ld.lld >/dev/null 2>&1; then
  echo "lld not found." >&2
  echo "Install: sudo pacman -S lld" >&2
  echo "     or: sudo apt install lld" >&2
  missing=1
fi
if [[ "$missing" -ne 0 ]]; then
  exit 1
fi

if [[ $# -eq 1 ]]; then
  ver="$1"
  if [[ ! "$ver" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    usage
    exit 1
  fi
else
  current="$(sed -n 's/^  "version": "\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\)".*/\1/p' "$PKG" | head -n 1)"
  if [[ ! "$current" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "cannot read version from $PKG" >&2
    exit 1
  fi
  IFS=. read -r maj min pat <<<"$current"
  ver="${maj}.${min}.$((pat + 1))"
fi

sed -i -E "0,/\"version\": \"[^\"]+\"/s//\"version\": \"${ver}\"/" "$PKG"
sed -i -E "0,/^version = \"[^\"]+\"/s//version = \"${ver}\"/" "$CARGO"
sed -i -E "0,/\"version\": \"[^\"]+\"/s//\"version\": \"${ver}\"/" "$TAURI"

cd "$CLIENT"
npm run build
npx @tauri-apps/cli build --no-bundle
npx @tauri-apps/cli build --no-bundle --runner cargo-xwin --target x86_64-pc-windows-msvc

linux_bin="$CLIENT/src-tauri/target/release/modupdater-client"
windows_bin="$CLIENT/src-tauri/target/x86_64-pc-windows-msvc/release/modupdater-client.exe"
if [[ ! -f "$linux_bin" || ! -f "$windows_bin" ]]; then
  echo "build outputs not found" >&2
  echo "$linux_bin" >&2
  echo "$windows_bin" >&2
  exit 1
fi

token="${MODUPDATER_TOKEN:-}"
if [[ -z "$token" ]]; then
  if [[ ! -r /dev/tty ]]; then
    echo "MODUPDATER_TOKEN is not set" >&2
    exit 1
  fi
  read -r -p "username: " user </dev/tty
  read -r -s -p "password: " pass </dev/tty
  echo >&2
  login_body="$(python3 -c 'import json,sys; print(json.dumps({"username":sys.argv[1],"password":sys.argv[2]}))' "$user" "$pass")"
  resp="$(curl -sS -f -X POST "$SERVER_URL/api/auth/login" -H 'Content-Type: application/json' --data-binary "$login_body")"
  token="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])' <<<"$resp")"
fi

curl -sS -f -X POST "$SERVER_URL/api/admin/releases" \
  -H "Authorization: Bearer $token" \
  -F "version=${ver}" \
  -F "linux_file=@${linux_bin};filename=modupdater-linux" \
  -F "windows_file=@${windows_bin};filename=modupdater-windows.exe"
echo
