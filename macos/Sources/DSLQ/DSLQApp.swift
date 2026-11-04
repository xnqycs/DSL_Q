import AppKit
import SwiftUI

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    weak var model: AppModel?
    var reopenMainWindow: (() -> Void)?
    private var terminating = false
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard !terminating, let model else { return .terminateNow }
        terminating = true
        Task { await model.quit(); sender.reply(toApplicationShouldTerminate: true) }
        return .terminateLater
    }
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows: Bool) -> Bool {
        if !hasVisibleWindows { reopenMainWindow?() }
        return true
    }
}

@main
struct DSLQApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @StateObject private var model = AppModel()
    var body: some Scene {
        Window("DSL Q", id: "main") {
            MainView(model: model, delegate: delegate)
                .frame(minWidth: 900, minHeight: 570)
        }
        .defaultSize(width: 1180, height: 760)
        .commands { DownloadCommands(model: model) }
        MenuBarExtra("DSL Q", systemImage: "arrow.down.circle") { MenuContent(model: model) }
    }
}

struct DownloadCommands: Commands {
    @ObservedObject var model: AppModel
    @Environment(\.openWindow) private var openWindow
    var body: some Commands {
        CommandGroup(replacing: .newItem) { Button("新建下载") { model.windows.newDownload() }.keyboardShortcut("n") }
        CommandGroup(replacing: .appSettings) { Button("设置…") { openWindow(id: "main"); model.showSettings = true }.keyboardShortcut(",") }
    }
}

struct MenuContent: View {
    @ObservedObject var model: AppModel
    @Environment(\.openWindow) private var openWindow
    var body: some View {
        Text("\(model.activeCount) 个任务正在下载 · \(bytes(Int64(model.totalSpeed)))/s")
        Button("打开 DSL Q") { openWindow(id: "main"); NSApp.activate(ignoringOtherApps: true) }
        Button("新建下载…") { model.windows.newDownload() }
        Divider()
        Button("全部暂停") { for task in model.tasks where task.isActive || task.state == "queued" { model.action(task, "pause") } }
        Button("继续已暂停任务") { for task in model.tasks where task.state == "paused" { model.action(task, "resume") } }
        Toggle("下载时防止自动休眠", isOn: Binding(get: { model.settings.preventIdleSleepWhileDownloading }, set: model.toggleSleep))
        PowerStatus(power: model.power, enabled: model.settings.preventIdleSleepWhileDownloading)
        Divider()
        Button("设置…") { openWindow(id: "main"); NSApp.activate(ignoringOtherApps: true); model.showSettings = true }
        Button("退出 DSL Q") { NSApp.terminate(nil) }.keyboardShortcut("q")
    }
}
