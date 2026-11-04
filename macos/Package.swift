// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "DSLQ",
    platforms: [.macOS(.v14)],
    products: [.executable(name: "DSLQ", targets: ["DSLQ"])],
    targets: [
        .executableTarget(name: "DSLQ", linkerSettings: [.linkedFramework("SwiftUI"), .linkedFramework("IOKit"), .linkedFramework("Security")])
    ],
    swiftLanguageModes: [.v5]
)
