import AppKit
import SwiftUI

struct DownloadConfirmationView: View {
    @ObservedObject var session: DownloadConfirmation
    @ObservedObject var model: AppModel
    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 12) {
                Image(systemName: "arrow.down.doc.fill").font(.system(size: 32)).foregroundStyle(.tint)
                VStack(alignment: .leading, spacing: 4) {
                    Text("下载文件信息").font(.title2.weight(.semibold))
                    Text("确认保存位置后开始下载").foregroundStyle(.secondary)
                }
            }
            if session.taskID == nil {
                TextEditor(text: $session.links).font(.body.monospaced()).frame(height: 76).padding(6).background(.quaternary.opacity(0.3), in: RoundedRectangle(cornerRadius: 8)).accessibilityLabel("下载链接")
                    .disabled(session.working)
                Text(session.urls.count > 1 ? "共 \(session.urls.count) 个链接，当前确认第 1 项；其余项目随后逐个确认。" : "粘贴 HTTP / HTTPS 链接；多个链接每行一个。")
                    .font(.caption).foregroundStyle(.secondary)
            }
            HStack { Text("文件名").frame(width: 65, alignment: .leading); TextField("文件名", text: Binding(get: { session.filename }, set: { session.filename = $0; session.nameEdited = true })).disabled(session.working) }
            Grid(alignment: .leading, horizontalSpacing: 18, verticalSpacing: 10) {
                info("大小", session.preview.map { bytes($0.total) } ?? "未知")
                info("类型", session.preview?.contentType.isEmpty == false ? session.preview!.contentType : "未知")
                info("续传", session.preview.map { $0.resumable ? "支持" : "不支持或无需分段" } ?? "未知")
                info("来源", session.preview?.source ?? source)
            }.font(.callout)
            if session.loading { HStack { ProgressView().controlSize(.small); Text("正在获取文件信息…").font(.caption).foregroundStyle(.secondary) } }
            if let issue = session.previewError {
                HStack(alignment: .top) {
                    Text(issue + " 可重试，或直接开始下载。").font(.caption).foregroundStyle(.orange)
                    Spacer(); Button("重试") { Task { await preview() } }.disabled(session.loading)
                }
            }
            Divider()
            HStack {
                Text("保存到").foregroundStyle(.secondary)
                Text(session.directory).lineLimit(2).truncationMode(.middle).textSelection(.enabled)
                Spacer(); Button("选择…") { if let path = chooseDirectory(session.directory) { session.directory = path } }.disabled(session.working)
            }
            Picker("分段并发", selection: $session.connections) {
                Text("跟随设置（\(model.settings.connections) 路）").tag(0)
                ForEach(1...32, id: \.self) { Text("\($0) 路").tag($0) }
            }.disabled(session.working)
            if let error = session.error { Text(error).font(.callout).foregroundStyle(.orange) }
            Spacer(minLength: 0)
            HStack {
                Text("取消将停止这次下载").font(.caption).foregroundStyle(.secondary)
                Spacer()
                Button(session.urls.count > 1 ? "取消此项" : "取消") { model.windows.cancel(session) }.keyboardShortcut(.cancelAction).disabled(session.working)
                Button(session.working ? "正在处理…" : "开始下载") { model.windows.start(session) }.keyboardShortcut(.defaultAction).buttonStyle(.borderedProminent)
                    .disabled(session.working || session.loading || !model.connected || (session.taskID == nil && !session.validURL) || session.filename.isEmpty)
            }
        }.padding(26)
        .task(id: session.currentURL) { await preview() }
    }
    var source: String {
        guard var components = URLComponents(string: session.currentURL) else { return "未知" }
        components.query = nil; components.fragment = nil; components.user = nil; components.password = nil
        return components.string ?? "未知"
    }
    private func info(_ key: String, _ value: String) -> some View {
        GridRow { Text(key).foregroundStyle(.secondary); Text(value).lineLimit(2).truncationMode(.middle).textSelection(.enabled) }
    }
    private func preview() async {
        guard session.taskID == nil else { return }
        session.previewGeneration += 1; let generation = session.previewGeneration; let url = session.currentURL
        session.preview = nil; session.previewError = nil; session.loading = false
        guard session.validURL else { return }
        if !session.nameEdited { session.filename = URL(string: url)?.lastPathComponent.removingPercentEncoding ?? "下载文件"; if session.filename.isEmpty { session.filename = "下载文件" } }
        session.loading = true
        do {
            try await Task.sleep(nanoseconds: 350_000_000)
            try Task.checkCancellation()
            let value = try await model.request("tasks.preview", ["url": url])
            guard generation == session.previewGeneration, !Task.isCancelled else { return }
            let result = try JSONDecoder().decode(DownloadPreview.self, from: JSONSerialization.data(withJSONObject: value))
            session.preview = result
            if !session.nameEdited { session.filename = result.name }
        } catch {
            guard generation == session.previewGeneration, !Task.isCancelled else { return }
            session.previewError = error.localizedDescription
        }
        if generation == session.previewGeneration { session.loading = false }
    }
}

@MainActor
func chooseDirectory(_ current: String) -> String? {
    let panel = NSOpenPanel(); panel.canChooseDirectories = true; panel.canChooseFiles = false; panel.canCreateDirectories = true; panel.allowsMultipleSelection = false
    panel.directoryURL = URL(fileURLWithPath: current); panel.prompt = "选择目录"
    return panel.runModal() == .OK ? panel.url?.path : nil
}

struct SettingsView: View {
    @ObservedObject var model: AppModel
    @Environment(\.dismiss) private var dismiss
    @ViewState private var draft = DownloadSettings()
    @ViewState private var speedKiB = 0
    @ViewState private var sizeMiB = 20
    @ViewState private var extensions = ""
    @ViewState private var excluded = ""
    @ViewState private var error: String?
    @ViewState private var saving = false
    var body: some View {
        VStack(spacing: 0) {
            HStack { Text("设置").font(.title2.weight(.semibold)); Spacer(); Button("完成") { save() }.keyboardShortcut(.defaultAction).disabled(saving) }.padding(24)
            Divider()
            Form {
                Section("下载") {
                    HStack { Text("默认保存位置"); Spacer(); Text(draft.directory).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle); Button("选择…") { if let p = chooseDirectory(draft.directory) { draft.directory = p } } }
                    Stepper("每个任务最多 \(draft.connections) 路分段", value: $draft.connections, in: 1...32)
                    Stepper("同时下载 \(draft.maxTasks) 个任务", value: $draft.maxTasks, in: 1...16)
                    HStack { Text("全局限速"); Spacer(); TextField("0", value: $speedKiB, format: .number).frame(width: 100).multilineTextAlignment(.trailing); Text("KiB/s，0 为不限速").foregroundStyle(.secondary) }
                }
                Section {
                    Toggle("下载时防止自动休眠", isOn: Binding(get: { model.settings.preventIdleSleepWhileDownloading }, set: model.toggleSleep))
                    PowerStatus(power: model.power, enabled: model.settings.preventIdleSleepWhileDownloading)
                    Text("仅在下载、重试和保存文件时保持系统唤醒。允许屏幕熄灭，合盖和手动睡眠仍由系统处理。").font(.caption).foregroundStyle(.secondary)
                } header: { Text("电源") }
                Section("网络") {
                    TextField("HTTP / SOCKS5 代理（留空为直连）", text: $draft.proxy)
                    Text("例如 http://127.0.0.1:7890 或 socks5://127.0.0.1:7890；修改后用于下一次启动的下载。").font(.caption).foregroundStyle(.secondary)
                }
                Section("浏览器接管") {
                    Toggle("按规则自动接管浏览器下载", isOn: $draft.autoCapture)
                    HStack { Text("文件大小至少"); Spacer(); TextField("20", value: $sizeMiB, format: .number).frame(width: 80).multilineTextAlignment(.trailing); Text("MiB").foregroundStyle(.secondary) }
                    TextField("或匹配扩展名（逗号或空格分隔）", text: $extensions)
                    HStack {
                        Text("已配置 \(DownloadSettings.normalizedExtensions(extensions).count) 种文件类型").font(.caption).foregroundStyle(.secondary)
                        Spacer()
                        Button("补充常见类型") {
                            extensions = DownloadSettings.normalizedExtensions(extensions + " " + DownloadSettings.commonCaptureExtensions.joined(separator: " ")).joined(separator: ", ")
                        }
                    }
                    TextField("排除站点（逗号分隔，如 example.com）", text: $excluded)
                    HStack {
                        Button("安装连接组件并导出扩展") { model.installBrowserHosts() }
                        Button("复制扩展路径") { model.copyExtensionPath() }
                        Button("移除连接组件") { model.uninstallBrowserHosts() }
                    }
                    Text(model.integrationStatus).font(.caption).foregroundStyle(.secondary)
                    Text("在 Chrome 扩展管理页开启开发者模式，选择“加载已解压的扩展程序”，再选择“下载”中的“DSL Q Chrome 扩展”文件夹。请保留此文件夹，Chrome 会从这里加载扩展。也可复制路径，在选择窗口按 ⌘⇧G 粘贴。").font(.caption).foregroundStyle(.secondary)
                    Button("打开 Chrome 扩展管理") { openBrowser("Google Chrome", "chrome://extensions") }
                }
                Section("通知") {
                    Toggle("下载开始时通知", isOn: $draft.notifyOnStart)
                    Toggle("下载结束时通知（完成、失败或取消）", isOn: $draft.notifyOnFinish)
                    NotificationSettingsView(notifications: model.notifications)
                }
                if let error { Section { Text(error).foregroundStyle(.orange) } }
            }.formStyle(.grouped)
        }.frame(width: 660, height: 710)
        .onAppear { draft = model.settings; speedKiB = Int(draft.speedLimit / 1024); sizeMiB = Int(draft.captureMinBytes / (1024 * 1024)); extensions = draft.captureExtensions.joined(separator: ", "); excluded = draft.excludedHosts.joined(separator: ", ") }
    }
    private func save() {
        guard speedKiB >= 0, sizeMiB >= 0, speedKiB < Int.max / 1024, sizeMiB < Int.max / (1024 * 1024) else { error = "限速和接管阈值必须是有效的非负数"; return }
        draft.speedLimit = Int64(speedKiB) * 1024; draft.captureMinBytes = Int64(sizeMiB) * 1024 * 1024
        draft.captureExtensions = DownloadSettings.normalizedExtensions(extensions)
        draft.excludedHosts = excluded.split(separator: ",").map { $0.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() }.filter { !$0.isEmpty }
        draft.preventIdleSleepWhileDownloading = model.settings.preventIdleSleepWhileDownloading
        saving = true
        Task { do { try await model.saveSettings(draft); dismiss() } catch { self.error = error.localizedDescription }; saving = false }
    }
    private func openBrowser(_ app: String, _ url: String) {
        let process = Process(); process.executableURL = URL(fileURLWithPath: "/usr/bin/open"); process.arguments = ["-a", app, url]
        do { try process.run() } catch { model.message = "未找到 \(app)，请先安装浏览器。" }
    }
}
