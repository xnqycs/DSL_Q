import AppKit
import SwiftUI
import UserNotifications

@MainActor
final class AppModel: ObservableObject {
    @Published var tasks: [DownloadTask] = []
    @Published var settings = DownloadSettings()
    @Published var connected = false
    @Published var startupError: String?
    @Published var message: String?
    @Published var showAdd = false
    @Published var showSettings = false
    @Published var integrationStatus = "尚未安装浏览器连接组件"
    let power = PowerController()
    private var process: Process?
    private var input: FileHandle?
    private var output: FileHandle?
    private var buffer = Data()
    private var pending: [String: CheckedContinuation<Any, Error>] = [:]
    private var quitting = false
    private var completedIDs: Set<String> = []
    private var receivedFirstSnapshot = false
    var activeCount: Int { tasks.filter(\.isActive).count }
    var totalSpeed: Double { tasks.reduce(0) { $0 + $1.speed } }
    var extensionDirectory: URL { Bundle.main.bundleURL.appendingPathComponent("Contents/Resources/extension") }
    var dataDirectory: URL {
        if ProcessInfo.processInfo.arguments.contains("--integration-test"), let path = ProcessInfo.processInfo.environment["DSLQ_TEST_DATA_DIR"] { return URL(fileURLWithPath: path) }
        return FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support/DSL Q")
    }

    func start() {
        guard process == nil else { return }
        startupError = nil; quitting = false; buffer.removeAll()
        do {
            let key: Data
            if ProcessInfo.processInfo.arguments.contains("--integration-test"), let raw = ProcessInfo.processInfo.environment["DSLQ_TEST_KEY"], let testKey = Data(base64Encoded: raw), testKey.count == 32 { key = testKey }
            else { key = try DatabaseKey.load() }
            let child = Process()
            child.executableURL = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/dslq-core")
            child.arguments = ["--data-dir", dataDirectory.path]
            let stdin = Pipe(), stdout = Pipe(), stderr = Pipe()
            child.standardInput = stdin; child.standardOutput = stdout; child.standardError = stderr
            input = stdin.fileHandleForWriting; output = stdout.fileHandleForReading
            stdout.fileHandleForReading.readabilityHandler = { [weak self] file in
                let data = file.availableData
                Task { @MainActor [weak self] in self?.receive(data) }
            }
            stderr.fileHandleForReading.readabilityHandler = { file in _ = file.availableData }
            child.terminationHandler = { [weak self] _ in Task { @MainActor [weak self] in self?.disconnected() } }
            try child.run(); process = child
            Task {
                do {
                    let result = try await request("initialize", ["key": key.base64EncodedString()])
                    try apply(result)
                    if !quitting { connected = true; updatePower() }
                } catch { startupError = error.localizedDescription; child.terminate() }
            }
        } catch { startupError = error.localizedDescription; disconnected() }
    }

    private func receive(_ data: Data) {
        guard !data.isEmpty else { disconnected(); return }
        buffer.append(data)
        guard buffer.count < 16 * 1024 * 1024 else { message = "核心响应超过大小限制"; process?.terminate(); return }
        while let newline = buffer.firstIndex(of: 10) {
            let line = buffer[..<newline]; buffer.removeSubrange(...newline)
            guard let response = try? JSONSerialization.jsonObject(with: line) as? [String: Any] else { continue }
            if response["method"] as? String == "snapshot", let payload = response["params"] { try? apply(payload); continue }
            guard let id = response["id"] as? String, let continuation = pending.removeValue(forKey: id) else { continue }
            if let error = response["error"] as? [String: Any] { continuation.resume(throwing: DSLQError.message(error["message"] as? String ?? "操作失败")) }
            else { continuation.resume(returning: response["result"] ?? NSNull()) }
        }
    }

    func request(_ method: String, _ params: [String: Any] = [:]) async throws -> Any {
        guard let input else { throw DSLQError.message("下载核心尚未连接") }
        let id = UUID().uuidString
        let payload: [String: Any] = ["jsonrpc": "2.0", "protocol": 1, "id": id, "method": method, "params": params]
        var data = try JSONSerialization.data(withJSONObject: payload); data.append(10)
        return try await withCheckedThrowingContinuation { continuation in
            pending[id] = continuation
            do { try input.write(contentsOf: data) }
            catch { pending.removeValue(forKey: id)?.resume(throwing: error) }
            Task { [weak self] in
                try? await Task.sleep(nanoseconds: 35_000_000_000)
                self?.pending.removeValue(forKey: id)?.resume(throwing: DSLQError.message("下载核心响应超时"))
            }
        }
    }

    private func apply(_ object: Any) throws {
        let snapshot = try JSONDecoder().decode(EngineSnapshot.self, from: JSONSerialization.data(withJSONObject: object))
        guard snapshot.protocol == 1 else { throw DSLQError.message("下载核心版本不匹配") }
        let nowComplete = Set(snapshot.tasks.filter { $0.state == "completed" }.map(\.id))
        if receivedFirstSnapshot {
            for task in snapshot.tasks where nowComplete.subtracting(completedIDs).contains(task.id) { notify(task) }
        }
        receivedFirstSnapshot = true; completedIDs = nowComplete
        tasks = snapshot.tasks; settings = snapshot.settings
        updatePower()
    }

    private func notify(_ task: DownloadTask) {
        let name = task.name, id = task.id
        UNUserNotificationCenter.current().getNotificationSettings { settings in
            guard settings.authorizationStatus == .authorized else { return }
            let content = UNMutableNotificationContent(); content.title = "下载完成"; content.body = name
            UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: id, content: content, trigger: nil))
        }
    }

    func enableNotifications() { UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { _, _ in } }
    func updatePower() { power.update(enabled: settings.preventIdleSleepWhileDownloading, activeCount: activeCount, connected: connected && !quitting) }

    private func disconnected() {
        output?.readabilityHandler = nil; output = nil; input = nil; process = nil
        connected = false; power.update(enabled: false, activeCount: 0, connected: false)
        let old = pending; pending.removeAll()
        for continuation in old.values { continuation.resume(throwing: DSLQError.message("下载核心已断开连接")) }
        if !quitting && startupError == nil { startupError = "下载核心已停止，点击重新连接可恢复任务。" }
    }

    func perform(_ method: String, _ params: [String: Any] = [:]) {
        Task { do { _ = try await request(method, params) } catch { message = error.localizedDescription } }
    }
    func action(_ task: DownloadTask, _ action: String) { perform("tasks.action", ["id": task.id, "action": action]) }
    func saveSettings(_ value: DownloadSettings) async throws {
        _ = try await request("settings.update", try dictionary(value)); settings = value; updatePower()
    }
    func toggleSleep(_ enabled: Bool) {
        var next = settings; next.preventIdleSleepWhileDownloading = enabled
        Task { do { try await saveSettings(next) } catch { message = error.localizedDescription } }
    }

    func installBrowserHosts() {
        do {
            let manifestURL = extensionDirectory.appendingPathComponent("extension-id.txt")
            let extensionID = try String(contentsOf: manifestURL, encoding: .utf8).trimmingCharacters(in: .whitespacesAndNewlines)
            let host = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/dslq-host").path
            let config: [String: Any] = ["name": "local.dslq.download", "description": "DSL Q 下载助手", "path": host, "type": "stdio", "allowed_origins": ["chrome-extension://\(extensionID)/"]]
            let content = try JSONSerialization.data(withJSONObject: config, options: [.prettyPrinted, .sortedKeys])
            let base = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support")
            let directory = base.appendingPathComponent("Google/Chrome/NativeMessagingHosts")
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            try content.write(to: directory.appendingPathComponent("local.dslq.download.json"), options: .atomic)
            integrationStatus = "Chrome 连接组件已安装"
            NSWorkspace.shared.open(extensionDirectory)
        } catch { message = error.localizedDescription }
    }

    func uninstallBrowserHosts() {
        let base = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support")
        try? FileManager.default.removeItem(at: base.appendingPathComponent("Google/Chrome/NativeMessagingHosts/local.dslq.download.json"))
        integrationStatus = "连接组件已移除"
    }

    func quit() async {
        quitting = true; updatePower()
        if let child = process, child.isRunning {
            do { _ = try await request("shutdown") } catch { if child.isRunning { child.terminate() } }
        }
    }
}
