#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
go test -race ./...
(cd extension && npm run typecheck && npm test)
mkdir -p .cache
swift build --package-path macos
swiftc -parse-as-library -target arm64-apple-macos14.0 macos/Sources/DSLQ/Models.swift macos/Sources/DSLQ/PowerController.swift macos/Checks/PowerChecks.swift -o .cache/power-checks
.cache/power-checks
