import Foundation
import SwiftUI

// Explicitly select the property wrapper. The macOS 27 SDK also exports an
// identically named macro whose plugin isn't shipped in Command Line Tools.
typealias ViewState<Value> = SwiftUI.State<Value>

struct DownloadSettings: Codable, Equatable {
    var notifyOnStart = true
    var notifyOnFinish = true
    var connections = 8
    var maxTasks = 3
    var speedLimit: Int64 = 0
    var directory = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Downloads").path
    var proxy = ""
    var preventIdleSleepWhileDownloading = false
    var autoCapture = false
    var captureMinBytes: Int64 = 20 * 1024 * 1024
    static let commonCaptureExtensions = """
zip 7z rar tar gz bz2 xz zst tgz tbz2 txz lz lzma lz4 cab
dmg pkg iso img exe msi msix apk deb rpm appimage
pdf doc docx xls xlsx ppt pptx odt ods odp rtf txt csv tsv epub mobi azw3
mp3 m4a aac wav flac ogg opus mp4 mkv mov avi webm m4v mpeg mpg ts
jpg jpeg png gif webp heic heif avif tif tiff bmp svg ico psd
json yaml yml xml toml ini log sql db sqlite bak patch diff jar war wasm bin
""".split(whereSeparator: { $0.isWhitespace }).map(String.init)
    var captureExtensions = Self.commonCaptureExtensions

    static func normalizedExtensions(_ text: String) -> [String] {
        var seen = Set<String>()
        return text.lowercased().split(whereSeparator: { $0.isWhitespace || ",，;；".contains($0) }).compactMap {
            let value = String($0.drop(while: { $0 == "." }))
            return !value.isEmpty && seen.insert(value).inserted ? value : nil
        }
    }
    var excludedHosts: [String] = []
}

struct DownloadChunk: Codable { let start: Int64; let end: Int64; let done: Bool }
struct DownloadTask: Codable, Identifiable {
    let contentType: String?
    let runId: String?
    let started: Bool?
    let id: String
    let name: String
    let source: String
    let path: String
    let state: String
    let total: Int64
    let received: Int64
    let speed: Double
    let connections: Int
    let activeConnections: Int
    let chunks: [DownloadChunk]
    let error: String
    let createdAt: String
    let resumable: Bool
    var isActive: Bool { ["probing", "downloading", "retrying", "finishing"].contains(state) }
    var terminal: Bool { ["completed", "failed", "cancelled"].contains(state) }
    var awaitingConfirmation: Bool { state == "awaiting_confirmation" }
    var runKey: String { id + ":" + (runId ?? "legacy") }
    var progress: Double { total > 0 ? min(1, max(0, Double(received) / Double(total))) : (state == "completed" ? 1 : 0) }
    var stateLabel: String {
        ["awaiting_confirmation": "等待确认", "cancelling": "正在取消", "cancelled": "已取消", "queued": "等待中", "probing": "连接中", "downloading": "下载中", "retrying": "正在重试", "finishing": "正在保存", "paused": "已暂停", "failed": "下载失败", "completed": "已完成", "prepared": "等待浏览器交接"][state] ?? state
    }
    var remaining: String {
        guard speed > 1, total > received else { return "—" }
        let seconds = Int(ceil(Double(total - received) / speed))
        if seconds < 60 { return "\(seconds) 秒" }
        if seconds < 3600 { return "\(seconds / 60) 分钟" }
        return "\(seconds / 3600) 小时 \((seconds % 3600) / 60) 分钟"
    }
}
struct EngineSnapshot: Codable { let tasks: [DownloadTask]; let settings: DownloadSettings; let activeCount: Int; let `protocol`: Int }

func bytes(_ value: Int64) -> String {
    guard value >= 0 else { return "大小未知" }
    if value == 0 { return "0 B" }
    return ByteCountFormatter.string(fromByteCount: value, countStyle: .binary)
}

func dictionary<T: Encodable>(_ value: T) throws -> [String: Any] {
    try JSONSerialization.jsonObject(with: JSONEncoder().encode(value)) as! [String: Any]
}

enum DSLQError: LocalizedError {
    case message(String)
    var errorDescription: String? { if case .message(let text) = self { return text }; return nil }
}

struct DownloadPreview: Codable {
    let name: String
    let total: Int64
    let contentType: String
    let source: String
    let resumable: Bool
}

@MainActor
final class DownloadConfirmation: ObservableObject, Identifiable {
    let id: String
    let taskID: String?
    @Published var links: String
    @Published var filename: String
    @Published var directory: String
    @Published var connections: Int
    @Published var preview: DownloadPreview?
    @Published var previewError: String?
    @Published var loading = false
    @Published var working = false
    @Published var error: String?
    var nameEdited = false
    var previewGeneration = 0
    var urls: [String] { links.components(separatedBy: .newlines).map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.filter { !$0.isEmpty } }
    var currentURL: String { urls.first ?? "" }
    var validURL: Bool {
        guard let url = URL(string: currentURL) else { return false }
        return ["http", "https"].contains(url.scheme ?? "") && url.host != nil && url.user == nil && url.password == nil
    }
    init(task: DownloadTask, settings: DownloadSettings) {
        id = task.id; taskID = task.id; links = ""; filename = task.name
        directory = settings.directory; connections = task.connections
        preview = DownloadPreview(name: task.name, total: task.total, contentType: task.contentType ?? "", source: task.source, resumable: task.resumable)
    }
    init(url: String = "", settings: DownloadSettings) {
        id = UUID().uuidString; taskID = nil; links = url; filename = ""; directory = settings.directory; connections = 0
    }
}
