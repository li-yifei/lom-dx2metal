#!/bin/bash
# 构建 GUI：build/dx2metal GUI.app（仅 arm64，ad-hoc 签名）。
# GUI 在进程内直接调用 core 包（与 CLI 同一 module），.app 是自包含的单个二进制。
#
#   ./build.sh            构建并测试
#   ./build.sh -skip-dmg  传给 mygo build 的额外参数
set -euo pipefail
GUI="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$GUI/env.sh"
cd "$GUI"

go tool mygo generate
go vet .
go test .
rm -rf "$GUI/build"
go tool mygo build -platform darwin/arm64 -skip-dmg "$@"

name=$(/usr/bin/python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["name"])' "$GUI/mygo.json")
built="$GUI/build/darwin-arm64/$name.app"
app="$GUI/build/$name.app"
rm -rf "$app" && mv "$built" "$app" && rmdir "$GUI/build/darwin-arm64" 2>/dev/null || true
# 移动后重新签名（ad-hoc），保证签名与最终位置一致。
codesign --force --deep --sign - "$app"
echo "--- $(lipo -archs "$app/Contents/MacOS/$name") · $(du -sh "$app" | cut -f1)"
codesign --verify --deep --strict "$app" && codesign -dv "$app" 2>&1 | grep -E 'Signature|Identifier'
echo "$app"
