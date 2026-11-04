#!/bin/bash
set -euo pipefail
project_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$project_root"

version="$(cat VERSION)"
label="${1:-v$version}"
if [[ ! "$label" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
  printf 'Invalid package label: %s\n' "$label" >&2
  exit 1
fi
if ! git diff --quiet HEAD -- || [[ -n "$(git ls-files --others --exclude-standard)" ]]; then
  printf 'Commit source changes before packaging so the source archive matches the build.\n' >&2
  exit 1
fi

app_dir="$project_root/dist/DSL Q.app"
actual_version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app_dir/Contents/Info.plist")"
if [[ "$actual_version" != "$version" ]]; then
  printf 'App version differs from VERSION; run scripts/build.sh first.\n' >&2
  exit 1
fi
codesign --verify --deep --strict "$app_dir"
for binary in Contents/MacOS/DSLQ Contents/Helpers/dslq-core Contents/Helpers/dslq-host; do
  if [[ "$(lipo -archs "$app_dir/$binary")" != arm64 ]]; then
    printf 'Expected an Apple Silicon binary: %s\n' "$binary" >&2
    exit 1
  fi
done
for notice in LICENSE NOTICE THIRD_PARTY_NOTICES.md; do
  cmp "$notice" "$app_dir/Contents/Resources/$notice"
  cmp "$notice" "$app_dir/Contents/Resources/extension/$notice"
done

output="$project_root/dist/packages/$label"
if [[ -e "$output" ]]; then
  printf 'Package output already exists; use a fresh label or remove it explicitly: %s\n' "$output" >&2
  exit 1
fi
mkdir -p "$output" "$project_root/.cache"
stage="$(mktemp -d "$project_root/.cache/package.XXXXXX")"
trap 'rm -rf "$stage"' EXIT

# ditto preserves the executable bits, bundle layout, and code signatures.
ditto -c -k --sequesterRsrc --keepParent "$app_dir" "$output/DSL-Q-$label-macos-arm64.zip"
ditto "$app_dir/Contents/Resources/extension" "$stage/DSL-Q-Chrome-Extension"
ditto -c -k --norsrc --keepParent "$stage/DSL-Q-Chrome-Extension" "$output/DSL-Q-$label-chrome-extension.zip"
git archive --format=tar.gz --prefix="DSL_Q-$label/" --output="$output/DSL-Q-$label-source.tar.gz" HEAD
(
  cd "$output"
  shasum -a 256 ./*.zip ./*.tar.gz > SHA256SUMS
  shasum -a 256 -c SHA256SUMS
)
printf 'Packages: %s\n' "$output"
