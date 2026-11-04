import AppKit
import SwiftUI

struct MainView: View {
    @ObservedObject var model: AppModel
    let delegate: AppDelegate
    @Environment(\.openWindow) private var openWindow
    @ViewState private var filter: String? = "all"
    @ViewState private var selected: String?
    @ViewState private var search = ""
    @ViewState private var showInspector = true
    @ViewState private var deleting: DownloadTask?
    private let filters: [(String, String, String)] = [("all", "全部任务", "tray.full"), ("active", "正在下载", "arrow.down.circle"), ("paused", "已暂停", "pause.circle"), ("completed", "已完成", "checkmark.circle"), ("failed", "下载失败", "exclamationmark.circle")]
    var filtered: [DownloadTask] {
        model.tasks.filter { t in
            let matches = filter == "all" || filter == nil || (filter == "active" ? t.isActive || t.state == "queued" || t.state == "prepared" || t.awaitingConfirmation : t.state == filter)
            return matches && (search.isEmpty || t.name.localizedCaseInsensitiveContains(search) || t.source.localizedCaseInsensitiveContains(search))
        }.reversed()
    }
    var selection: DownloadTask? { model.tasks.first { $0.id == selected } }
    var body: some View {
        NavigationSplitView {
            List(selection: $filter) {
                Section("下载资料库") {
                    ForEach(filters, id: \.0) { item in
                        Label(item.1, systemImage: item.2).tag(item.0).padding(.vertical, 5)
                    }
                }
                Section {
                    Button { model.showSettings = true } label: { Label("设置与浏览器集成", systemImage: "gearshape") }.buttonStyle(.plain)
                }
            }
            .navigationSplitViewColumnWidth(min: 175, ideal: 190, max: 240)
            .safeAreaInset(edge: .bottom) {
                VStack(alignment: .leading, spacing: 6) {
                    Text("DSL Q").font(.title3.weight(.semibold))
                    Text("每一次下载，都清晰可见").font(.caption).foregroundStyle(.secondary)
                }.frame(maxWidth: .infinity, alignment: .leading).padding(18)
            }
        } detail: {
            VStack(spacing: 0) {
                if let error = model.startupError {
                    HStack { Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange); Text(error); Spacer(); Button("重新连接") { model.start() } }
                        .padding(12).background(.orange.opacity(0.09))
                }
                HSplitView {
                    Group {
                        if filtered.isEmpty {
                            ContentUnavailableView {
                                Label(model.tasks.isEmpty ? "开始你的第一项下载" : "没有匹配的任务", systemImage: "arrow.down.doc")
                            } description: {
                                Text(model.tasks.isEmpty ? "粘贴文件链接，或从 Chrome 发送下载。" : "试试其他筛选条件或搜索关键词。")
                            } actions: {
                                Button("新建下载") { model.windows.newDownload() }.buttonStyle(.borderedProminent).disabled(!model.connected)
                            }
                        } else { taskTable }
                    }.frame(minWidth: 470, maxWidth: .infinity, maxHeight: .infinity)
                    if showInspector, let task = selection { TaskDetail(task: task, model: model).frame(minWidth: 250, idealWidth: 285, maxWidth: 340) }
                }
                Divider()
                HStack(spacing: 16) {
                    Circle().fill(model.connected ? Color.green : Color.orange).frame(width: 6, height: 6)
                    Text(model.connected ? "\(model.tasks.count) 个任务" : "正在连接下载核心")
                    Spacer()
                    PowerStatus(power: model.power, enabled: model.settings.preventIdleSleepWhileDownloading)
                    Label("\(bytes(Int64(model.totalSpeed)))/s", systemImage: "arrow.down").monospacedDigit()
                }.font(.caption).foregroundStyle(.secondary).padding(.horizontal, 16).padding(.vertical, 11)
            }
            .navigationTitle(filters.first { $0.0 == filter }?.1 ?? "全部任务")
            .toolbar {
                ToolbarItemGroup {
                    Button { model.windows.newDownload() } label: { Label("新建下载", systemImage: "plus") }.help("新建下载 ⌘N").disabled(!model.connected)
                    if let task = selection, task.awaitingConfirmation {
                        Button("确认下载") { model.windows.showConfirmation(task.id) }
                    } else if let task = selection, task.state == "cancelled" {
                        Button("从头下载") { model.action(task, "restart") }
                    } else if let task = selection, task.state != "completed" {
                        Button { model.action(task, task.isActive || task.state == "queued" ? "pause" : "resume") } label: {
                            Label(task.isActive || task.state == "queued" ? "暂停" : "继续", systemImage: task.isActive || task.state == "queued" ? "pause" : "play")
                        }.disabled(task.state == "prepared" || task.state == "cancelling")
                    }
                    Button { showInspector.toggle() } label: { Label("任务详情", systemImage: "sidebar.right") }
                }
            }
            .searchable(text: $search, prompt: "搜索名称或来源")
        }
        .background(MainWindowObserver(delegate: delegate))
        .onAppear { [delegate] in
            delegate.model = model
            let action = openWindow
            delegate.reopenMainWindow = { action(id: "main") }
            model.reopenMainWindow = { [weak delegate] in delegate?.showMainWindow() }
            model.start()
        }
        .sheet(isPresented: $model.showSettings) { SettingsView(model: model) }
        .alert("操作未完成", isPresented: Binding(get: { model.message != nil }, set: { if !$0 { model.message = nil } })) { Button("好") { model.message = nil } } message: { Text(model.message ?? "") }
        .confirmationDialog("移除下载任务？", isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } }), titleVisibility: .visible) {
            Button("移除任务", role: .destructive) { if let task = deleting { model.perform("tasks.delete", ["id": task.id]) }; deleting = nil }
        } message: { Text("已完成的文件会保留。未完成任务的临时文件将被删除。") }
    }

    var taskTable: some View {
        Table(filtered, selection: $selected) {
            TableColumn("文件") { task in
                HStack(spacing: 10) {
                    Image(systemName: task.state == "completed" ? "doc.fill" : "doc.badge.arrow.down").font(.title2).foregroundStyle(task.state == "failed" ? Color.orange : Color.accentColor)
                    VStack(alignment: .leading, spacing: 5) {
                        Text(task.name).fontWeight(.medium).lineLimit(1)
                        Text(URL(string: task.source)?.host ?? task.source).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                    }
                }.padding(.vertical, 7)
            }.width(min: 180, ideal: 260)
            TableColumn("进度") { task in
                VStack(alignment: .leading, spacing: 6) {
                    HStack { Text(task.stateLabel); Spacer(); if task.total > 0 { Text(task.progress, format: .percent.precision(.fractionLength(0))).monospacedDigit() } }.font(.caption)
                    ProgressView(value: task.progress).tint(task.state == "failed" ? .orange : .accentColor)
                }
            }.width(min: 120, ideal: 155)
            TableColumn("大小") { task in Text(bytes(task.total)).font(.caption).monospacedDigit().foregroundStyle(.secondary) }.width(90)
            TableColumn("速度") { task in Text(task.isActive ? "\(bytes(Int64(task.speed)))/s" : "—").font(.caption).monospacedDigit() }.width(95)
        }
        .contextMenu(forSelectionType: String.self) { ids in
            if let id = ids.first, let task = model.tasks.first(where: { $0.id == id }) {
                Button(task.awaitingConfirmation ? "确认下载…" : "显示进度窗口") { model.windows.openProgress(task.id) }.disabled(task.state == "prepared")
                if task.awaitingConfirmation {
                    Button("取消下载", role: .destructive) { model.action(task, "cancel") }
                } else if task.state == "cancelled" {
                    Button("从头下载") { model.action(task, "restart") }
                } else if task.state == "completed" {
                    Button("在 Finder 中显示") { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: task.path)]) }
                } else if task.state != "prepared" && task.state != "cancelling" {
                    Button(task.isActive ? "暂停" : "继续下载") { model.action(task, task.isActive ? "pause" : "resume") }
                    if task.state == "failed" || task.state == "paused" { Button("从头下载") { model.action(task, "restart") } }
                }
                Divider()
                Button("移除任务…", role: .destructive) { deleting = task }.disabled(task.state == "prepared" || task.state == "cancelling")
            }
        } primaryAction: { ids in
            if let id = ids.first { model.windows.openProgress(id) }
        }
    }
}

struct PowerStatus: View {
    @ObservedObject var power: PowerController
    let enabled: Bool
    var body: some View {
        Label(power.failure != nil ? "防休眠未生效" : power.holding ? "下载期间保持唤醒" : enabled ? "防休眠已开启 · 等待任务" : "遵循系统休眠设置", systemImage: power.holding ? "moon.zzz.fill" : "moon")
            .foregroundStyle(power.failure != nil ? Color.orange : Color.secondary)
            .help(power.failure ?? "允许屏幕熄灭；合盖和手动睡眠仍由系统处理。")
    }
}

struct TaskDetail: View {
    let task: DownloadTask
    @ObservedObject var model: AppModel
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                VStack(alignment: .leading, spacing: 10) {
                    Image(systemName: "arrow.down.document.fill").font(.system(size: 36)).foregroundStyle(.tint)
                    Text(task.name).font(.title3.weight(.semibold)).textSelection(.enabled)
                    Text(task.stateLabel).font(.subheadline).foregroundStyle(.secondary)
                }
                Divider()
                Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 13) {
                    row("已下载", bytes(task.received)); row("总大小", bytes(task.total)); row("剩余时间", task.remaining)
                    row("活动连接", "\(task.activeConnections)"); row("断点续传", task.resumable ? "支持" : "不支持或尚未确认")
                }.font(.callout)
                VStack(alignment: .leading, spacing: 8) {
                    Text("分段并发").font(.subheadline.weight(.medium))
                    Picker("分段并发", selection: Binding(get: { task.connections }, set: { model.perform("tasks.connections", ["id": task.id, "connections": $0]) })) {
                        Text("跟随设置（\(model.settings.connections)）").tag(0)
                        ForEach(1...32, id: \.self) { Text("\($0) 路").tag($0) }
                    }.labelsHidden().disabled(task.state == "completed")
                    if !task.chunks.isEmpty {
                        LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 3), count: 8), spacing: 3) {
                            ForEach(Array(task.chunks.prefix(256).enumerated()), id: \.offset) { _, chunk in
                                RoundedRectangle(cornerRadius: 3).fill(chunk.done ? Color.accentColor : Color.secondary.opacity(0.15)).frame(height: 9)
                            }
                        }.accessibilityLabel("\(task.chunks.filter(\.done).count) / \(task.chunks.count) 个分段已完成")
                        Text("\(task.chunks.filter(\.done).count) / \(task.chunks.count) 个分段已完成").font(.caption).foregroundStyle(.secondary)
                    }
                }
                if !task.error.isEmpty { Label(task.error, systemImage: "exclamationmark.circle").font(.callout).foregroundStyle(.orange).textSelection(.enabled) }
                VStack(alignment: .leading, spacing: 6) {
                    Text("保存位置").font(.caption).foregroundStyle(.secondary)
                    Text(task.path).font(.callout).textSelection(.enabled)
                    Button("在 Finder 中显示") { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: task.path)]) }.disabled(task.state != "completed")
                }
                VStack(alignment: .leading, spacing: 6) { Text("来源").font(.caption).foregroundStyle(.secondary); Text(task.source).font(.caption).foregroundStyle(.secondary).textSelection(.enabled) }
            }.padding(22)
        }.background(.background)
    }
    func row(_ title: String, _ value: String) -> some View { GridRow { Text(title).foregroundStyle(.secondary); Text(value).monospacedDigit() } }
}
