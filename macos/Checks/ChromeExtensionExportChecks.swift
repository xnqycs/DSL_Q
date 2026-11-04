import Foundation

@main
struct ChromeExtensionExportChecks {
    static func main() throws {
        let files = FileManager.default
        let root = files.temporaryDirectory.appendingPathComponent("dslq-export-\(UUID().uuidString)")
        defer { try? files.removeItem(at: root) }
        let source = root.appendingPathComponent("DSL Q.app/Contents/Resources/extension")
        try files.createDirectory(at: source.appendingPathComponent("icons"), withIntermediateDirectories: true)
        let manifest = Data(#"{"manifest_version":3,"name":"test","version":"1.0","key":"stable-public-key"}"#.utf8)
        try manifest.write(to: source.appendingPathComponent("manifest.json"))
        try Data("fixed-extension-id".utf8).write(to: source.appendingPathComponent("extension-id.txt"))
        try Data("version-one".utf8).write(to: source.appendingPathComponent("background.js"))
        try Data([1,2,3]).write(to: source.appendingPathComponent("icons/icon.png"))
        let destination = ChromeExtensionExport.directory(home: root)
        precondition(!destination.pathComponents.contains(where: { $0.hasSuffix(".app") }))
        try ChromeExtensionExport.export(from: source, to: destination)
        let exportedManifest = try Data(contentsOf: destination.appendingPathComponent("manifest.json"))
        precondition(exportedManifest == manifest)
        let directoryValues = try destination.resourceValues(forKeys: [.isDirectoryKey, .isPackageKey])
        precondition(directoryValues.isDirectory == true && directoryValues.isPackage != true)
        precondition(files.fileExists(atPath: destination.appendingPathComponent("icons/icon.png").path))
        // Re-export updates the stable Chrome folder, preserves identity, and
        // does not delete unrelated user-created files in a managed export.
        try Data("user-note".utf8).write(to: destination.appendingPathComponent("note.txt"))
        try Data("version-two".utf8).write(to: source.appendingPathComponent("background.js"))
        try ChromeExtensionExport.export(from: source, to: destination)
        let updatedScript = try String(contentsOf: destination.appendingPathComponent("background.js"), encoding: .utf8)
        let updatedManifest = try Data(contentsOf: destination.appendingPathComponent("manifest.json"))
        precondition(updatedScript == "version-two")
        precondition(updatedManifest == manifest)
        precondition(files.fileExists(atPath: destination.appendingPathComponent("note.txt").path))
        let conflict = root.appendingPathComponent("unmanaged")
        try files.createDirectory(at: conflict, withIntermediateDirectories: true)
        try Data("keep".utf8).write(to: conflict.appendingPathComponent("important.txt"))
        var refused = false
        do { try ChromeExtensionExport.export(from: source, to: conflict) } catch { refused = true }
        precondition(refused && files.fileExists(atPath: conflict.appendingPathComponent("important.txt").path))
        precondition(!files.fileExists(atPath: conflict.appendingPathComponent("manifest.json").path))
        print("Chrome export checks passed: ordinary folder, payload/identity, repeat export, preservation and conflict protection")
    }
}
