#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "== go test ./... =="
(cd "$ROOT/server" && go test ./...)

echo "== npm test =="
(cd "$ROOT/client" && npm test)

echo "== cargo test =="
(cd "$ROOT" && cargo test --manifest-path client/src-tauri/Cargo.toml)

echo "smoke: ok"
