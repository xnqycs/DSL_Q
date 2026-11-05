#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
go test -race ./...
(cd extension && npm run typecheck && npm test)
mkdir -p .cache
check_target="$(uname -m)-apple-macos14.0"
swift build --package-path macos
swiftc -parse-as-library -target "$check_target" macos/Sources/DSLQ/Models.swift macos/Sources/DSLQ/PowerController.swift macos/Checks/PowerChecks.swift -o .cache/power-checks
.cache/power-checks
swiftc -parse-as-library -target "$check_target" macos/Sources/DSLQ/Models.swift macos/Sources/DSLQ/ChromeExtensionExport.swift macos/Checks/ChromeExtensionExportChecks.swift -o .cache/chrome-export-checks
.cache/chrome-export-checks
swiftc -parse-as-library -target "$check_target" macos/Sources/DSLQ/Models.swift macos/Sources/DSLQ/TaskNotifications.swift macos/Checks/NotificationChecks.swift -o .cache/notification-checks
.cache/notification-checks
swiftc -parse-as-library -D DSLQ_CHECKS -target "$check_target" macos/Sources/DSLQ/*.swift macos/Checks/SnapshotChecks.swift -o .cache/snapshot-checks
.cache/snapshot-checks
