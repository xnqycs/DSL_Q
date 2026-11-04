import Foundation

enum ChromeExtensionExport {
    static let folderName = "DSL Q Chrome 扩展"
    private static let markerName = ".dslq-extension-export"
    private static let marker = Data("local.dslq.chrome-extension\n".utf8)

    static func directory(home: URL = FileManager.default.homeDirectoryForCurrentUser) -> URL {
        home.appendingPathComponent("Downloads", isDirectory: true).appendingPathComponent(folderName, isDirectory: true)
    }

    @discardableResult
    static func export(from source: URL, to destination: URL) throws -> URL {
        let files = FileManager.default
        let manifestData = try Data(contentsOf: source.appendingPathComponent("manifest.json"))
        guard let manifest = try JSONSerialization.jsonObject(with: manifestData) as? [String: Any],
              manifest["manifest_version"] as? Int == 3,
              let key = manifest["key"] as? String, !key.isEmpty,
              files.fileExists(atPath: source.appendingPathComponent("extension-id.txt").path) else {
            throw DSLQError.message("Chrome 扩展资源不完整，请重新安装应用。")
        }
        // Read the payload before touching an existing export. Its public key
        // and ID are copied verbatim so Chrome keeps the same extension identity.
        var payload: [(String, Data)] = []
        func readDirectory(_ directory: URL, prefix: String) throws {
            let entries = try files.contentsOfDirectory(at: directory, includingPropertiesForKeys: [.isDirectoryKey, .isRegularFileKey, .isSymbolicLinkKey], options: [.skipsHiddenFiles])
            for file in entries {
                let values = try file.resourceValues(forKeys: [.isDirectoryKey, .isRegularFileKey, .isSymbolicLinkKey])
                guard values.isSymbolicLink != true else { throw DSLQError.message("扩展资源包含不支持的链接。") }
                let relative = prefix + file.lastPathComponent
                if values.isDirectory == true {
                    try readDirectory(file, prefix: relative + "/")
                } else if values.isRegularFile == true {
                    payload.append((relative, try Data(contentsOf: file)))
                }
            }
        }
        try readDirectory(source, prefix: "")
        let markerURL = destination.appendingPathComponent(markerName)
        if files.fileExists(atPath: destination.path) {
            guard (try? Data(contentsOf: markerURL)) == marker else {
                throw DSLQError.message("“下载”中已存在同名文件夹，尚未覆盖。请先将它重命名，再导出扩展。")
            }
        } else {
            try files.createDirectory(at: destination, withIntermediateDirectories: true)
            try marker.write(to: markerURL, options: .atomic)
        }
        for (relative, content) in payload {
            let target = destination.appendingPathComponent(relative)
            try files.createDirectory(at: target.deletingLastPathComponent(), withIntermediateDirectories: true)
            try content.write(to: target, options: .atomic)
        }
        return destination
    }
}
