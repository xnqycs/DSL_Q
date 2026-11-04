import AppKit
import SwiftUI

struct AddDownloadView: View {
    @ObservedObject var model: AppModel
    @Environment(\.dismiss) private var dismiss
    @ViewState private var links = ""
    @ViewState private var directory = ""
    @ViewState private var connections = 0
    @ViewState private var working = false
    @ViewState private var error: String?
    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack { Image(systemName: "arrow.down.circle.fill").font(.largeTitle).foregroundStyle(.tint); VStack(alignment: .leading, spacing: 3) { Text("新建下载").font(.title2.weight(.semibold)); Text("每行一个 HTTP 或 HTTPS 文件链接").foregroundStyle(.secondary) } }
            TextEditor(text: $links).font(.body.monospaced()).frame(height: 145).padding(8).background(.quaternary.opacity(0.3), in: RoundedRectangle(cornerRadius: 8)).accessibilityLabel("下载链接")
            HStack { Text("保存到").foregroundStyle(.secondary); Text(directory).lineLimit(1).truncationMode(.middle); Spacer(); Button("选择…") { if let path = chooseDirectory(directory) { directory = path } } }
            Picker("分段并发", selection: $connections) {
                Text("跟随设置（\(model.settings.connections) 路）").tag(0)
                ForEach(1...32, id: \.self) { Text("\($0) 路").tag($0) }
            }.frame(maxWidth: 280, alignment: .leading)
            if let error { Text(error).foregroundStyle(.orange).font(.callout) }
            HStack { Spacer(); Button("取消") { dismiss() }.keyboardShortcut(.cancelAction); Button(working ? "正在添加…" : "开始下载") { add() }.buttonStyle(.borderedProminent).keyboardShortcut(.defaultAction).disabled(working || links.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !model.connected) }
        }.padding(26).frame(width: 610)
        .onAppear { directory = model.settings.directory }
    }
    private func add() {
        let urls = links.components(separatedBy: .newlines).map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.filter { !$0.isEmpty }
        guard urls.allSatisfy({ guard let u = URL(string: $0) else { return false }; return ["http", "https"].contains(u.scheme ?? "") && u.host != nil }) else { error = "请检查链接，只支持 HTTP / HTTPS 文件地址。"; return }
        working = true; error = nil
        Task {
            var remaining = urls
            do {
                for url in urls { _ = try await model.request("tasks.add", ["url": url, "directory": directory, "connections": connections]); remaining.removeFirst() }
                dismiss()
            } catch { links = remaining.joined(separator: "\n"); self.error = error.localizedDescription }
            working = false
        }
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
                    TextField("或匹配扩展名（逗号分隔）", text: $extensions)
                    TextField("排除站点（逗号分隔，如 example.com）", text: $excluded)
                    HStack { Button("安装连接组件并显示扩展") { model.installBrowserHosts() }; Button("移除连接组件") { model.uninstallBrowserHosts() } }
                    Text(model.integrationStatus).font(.caption).foregroundStyle(.secondary)
                    Text("在 Chrome 扩展管理页开启开发者模式，选择“加载已解压的扩展程序”，载入刚打开的文件夹。在扩展弹窗中检查连接状态，并授权网站访问。").font(.caption).foregroundStyle(.secondary)
                    Button("打开 Chrome 扩展管理") { openBrowser("Google Chrome", "chrome://extensions") }
                }
                Section("通知") { Button("允许下载完成通知") { model.enableNotifications() } }
                if let error { Section { Text(error).foregroundStyle(.orange) } }
            }.formStyle(.grouped)
        }.frame(width: 660, height: 710)
        .onAppear { draft = model.settings; speedKiB = Int(draft.speedLimit / 1024); sizeMiB = Int(draft.captureMinBytes / (1024 * 1024)); extensions = draft.captureExtensions.joined(separator: ", "); excluded = draft.excludedHosts.joined(separator: ", ") }
    }
    private func save() {
        guard speedKiB >= 0, sizeMiB >= 0, speedKiB < Int.max / 1024, sizeMiB < Int.max / (1024 * 1024) else { error = "限速和接管阈值必须是有效的非负数"; return }
        draft.speedLimit = Int64(speedKiB) * 1024; draft.captureMinBytes = Int64(sizeMiB) * 1024 * 1024
        draft.captureExtensions = extensions.split(separator: ",").map { $0.trimmingCharacters(in: .whitespacesAndNewlines).lowercased().trimmingCharacters(in: CharacterSet(charactersIn: ".")) }.filter { !$0.isEmpty }
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
