import AppKit
import SwiftUI

struct DownloadProgressView: View {
    let taskID: String
    @ObservedObject var model: AppModel
    var task: DownloadTask? { model.tasks.first { $0.id == taskID } }
    var body: some View {
        Group {
            if let task {
                VStack(alignment: .leading, spacing: 18) {
                    HStack(alignment: .top, spacing: 12) {
                        Image(systemName: task.state == "completed" ? "checkmark.circle.fill" : "arrow.down.circle.fill").font(.system(size: 34)).foregroundStyle(task.state == "failed" ? Color.orange : Color.accentColor)
                        VStack(alignment: .leading, spacing: 5) {
                            Text(task.name).font(.title2.weight(.semibold)).lineLimit(2).textSelection(.enabled)
                            Text(task.stateLabel).foregroundStyle(.secondary)
                        }
                        Spacer()
                    }
                    if task.total >= 0 { ProgressView(value: task.progress).accessibilityLabel("下载进度") }
                    else if task.isActive { ProgressView().progressViewStyle(.linear).accessibilityLabel("总大小未知") }
                    else { ProgressView(value: 0).accessibilityLabel("总大小未知") }
                    HStack {
                        Text(task.total >= 0 || task.state == "completed" ? "\(Int(task.progress * 100))%" : "大小未知").font(.title.weight(.medium)).monospacedDigit()
                        Spacer(); Text("\(bytes(task.received)) / \(bytes(task.total))").monospacedDigit()
                    }
                    Grid(alignment: .leading, horizontalSpacing: 26, verticalSpacing: 12) {
                        row("下载速度", "\(bytes(Int64(task.speed)))/s")
                        row("剩余时间", task.remaining)
                        row("活动连接", "\(task.activeConnections)")
                        row("文件类型", task.contentType?.isEmpty == false ? task.contentType! : "未知")
                    }.font(.callout)
                    if !task.chunks.isEmpty {
                        LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 3), count: 16), spacing: 3) {
                            ForEach(Array(task.chunks.prefix(256).enumerated()), id: \.offset) { _, chunk in
                                RoundedRectangle(cornerRadius: 2).fill(chunk.done ? Color.accentColor : Color.secondary.opacity(0.15)).frame(height: 7)
                            }
                        }.accessibilityLabel("\(task.chunks.filter(\.done).count) / \(task.chunks.count) 个分段已完成")
                    }
                    if !task.error.isEmpty { Text(task.error).foregroundStyle(.orange).font(.callout).textSelection(.enabled) }
                    VStack(alignment: .leading, spacing: 5) {
                        Text("保存位置").font(.caption).foregroundStyle(.secondary)
                        Text(task.path).font(.callout).lineLimit(2).truncationMode(.middle).textSelection(.enabled)
                        Text(task.source).font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                    }
                    Spacer(minLength: 0)
                    HStack {
                        Text("关闭窗口不改变任务状态").font(.caption).foregroundStyle(.secondary)
                        Spacer()
                        if task.state == "completed" {
                            Button("在 Finder 中显示") { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: task.path)]) }
                            Button("打开文件") { NSWorkspace.shared.open(URL(fileURLWithPath: task.path)) }.buttonStyle(.borderedProminent)
                        } else {
                            if task.state == "cancelled" {
                                Button("从头下载") { model.action(task, "restart") }.buttonStyle(.borderedProminent)
                            } else if task.state != "cancelling" {
                                Button("取消下载", role: .destructive) { model.action(task, "cancel") }
                                Button(task.isActive || task.state == "queued" ? "暂停" : task.state == "failed" ? "重试下载" : "继续下载") { model.action(task, task.isActive || task.state == "queued" ? "pause" : "resume") }.buttonStyle(.borderedProminent)
                            }
                        }
                    }.disabled(!model.connected)
                }.padding(26)
            } else {
                ContentUnavailableView("等待任务状态", systemImage: "arrow.down.circle", description: Text("任务正在加载，或已从资料库移除。"))
            }
        }
    }
    private func row(_ label: String, _ value: String) -> some View { GridRow { Text(label).foregroundStyle(.secondary); Text(value).monospacedDigit() } }
}

struct NotificationSettingsView: View {
    @ObservedObject var notifications: TaskNotifications
    var body: some View {
        HStack {
            Text(notifications.permission == .authorized ? "系统通知已允许" : notifications.permission == .denied ? "系统通知未允许" : "尚未授权系统通知").font(.caption).foregroundStyle(.secondary)
            Spacer()
            if notifications.permission == .notDetermined {
                Button("允许通知") { notifications.requestIfNeeded() }
            } else {
                Button("打开通知设置") { NSWorkspace.shared.open(URL(string: "x-apple.systempreferences:com.apple.Notifications-Settings.extension")!) }
            }
        }.onAppear { notifications.refresh() }
        if let error = notifications.deliveryError { Text(error).font(.caption).foregroundStyle(.orange) }
    }
}
