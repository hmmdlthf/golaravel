#!/usr/bin/env bash
# Copies the sqlite3 WebAssembly module that drift ships into test/, where
# `flutter test --platform chrome` serves it at /sqlite3.wasm. Run after
# `flutter pub get`.
set -euo pipefail
cd "$(dirname "$0")/.."
drift_root=$(python3 -c '
import json, urllib.parse
for p in json.load(open(".dart_tool/package_config.json"))["packages"]:
    if p["name"] == "drift":
        print(urllib.parse.urlparse(p["rootUri"]).path)
')
cp "$drift_root/extension/devtools/build/sqlite3.wasm" test/sqlite3.wasm
echo "copied $drift_root/extension/devtools/build/sqlite3.wasm -> test/sqlite3.wasm"
