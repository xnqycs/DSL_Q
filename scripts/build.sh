#!/bin/bash
set -euo pipefail
project_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$project_root"
mkdir -p dist .cache
(cd extension && npm ci --no-audit --no-fund && npm run typecheck && npm run build)
extension_id="$(tr -d '\n' < extension/dist/extension-id.txt)"
swift build --package-path macos -c release
swift_bin_dir="$(swift build --package-path macos -c release --show-bin-path)"
app_dir="$project_root/dist/DSL Q.app"
mkdir -p "$app_dir/Contents/MacOS" "$app_dir/Contents/Helpers" "$app_dir/Contents/Resources"
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o "$app_dir/Contents/Helpers/dslq-core" ./cmd/dslq-core
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w -X main.extensionID=$extension_id" -o "$app_dir/Contents/Helpers/dslq-host" ./cmd/dslq-host
cp "$swift_bin_dir/DSLQ" "$app_dir/Contents/MacOS/DSLQ"
mkdir -p "$app_dir/Contents/Resources/extension"
cp extension/dist/* "$app_dir/Contents/Resources/extension/"
python3 - <<'PY'
import plistlib
from pathlib import Path
info={
 'CFBundleName':'DSL Q','CFBundleDisplayName':'DSL Q','CFBundleIdentifier':'local.dslq.app',
 'CFBundleExecutable':'DSLQ','CFBundlePackageType':'APPL','CFBundleShortVersionString':'0.2.1',
 'CFBundleVersion':'5','LSMinimumSystemVersion':'14.0','NSHighResolutionCapable':True,
 'NSPrincipalClass':'NSApplication','CFBundleIconFile':'DSLQ',
 'NSDownloadsFolderUsageDescription':'将你选择的下载文件保存到下载目录。',
}
Path('dist/DSL Q.app/Contents/Info.plist').write_bytes(plistlib.dumps(info))
PY
swift scripts/icon.swift .cache/AppIcon.iconset
iconutil -c icns .cache/AppIcon.iconset -o "$app_dir/Contents/Resources/DSLQ.icns"
codesign --force --sign - "$app_dir/Contents/Helpers/dslq-core"
codesign --force --sign - "$app_dir/Contents/Helpers/dslq-host"
codesign --force --sign - "$app_dir"
codesign --verify --deep --strict "$app_dir"
printf 'Built: %s\nExtension ID: %s\n' "$app_dir" "$extension_id"
