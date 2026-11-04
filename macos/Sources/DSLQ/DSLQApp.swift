import AppKit
import SwiftUI

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    weak var model: AppModel?
    var reopenMainWindow: (() -> Void)?
    private weak var mainWindow: NSWindow?
    private var windowGeneration = 0
    private var terminating = false

    func observeMainWindow(_ window: NSWindow) {
        guard mainWindow !== window else { return }
        let center = NotificationCenter.default
        if let mainWindow {
            center.removeObserver(self, name: NSWindow.willCloseNotification, object: mainWindow)
            center.removeObserver(self, name: NSWindow.didBecomeKeyNotification, object: mainWindow)
        }
        mainWindow = window
        center.addObserver(self, selector: #selector(mainWindowWillClose), name: NSWindow.willCloseNotification, object: window)
        center.addObserver(self, selector: #selector(mainWindowDidBecomeKey), name: NSWindow.didBecomeKeyNotification, object: window)
        restoreDockIcon()
    }

    private func restoreDockIcon() {
        windowGeneration += 1
        guard !terminating else { return }
        if NSApp.activationPolicy() != .regular { NSApp.setActivationPolicy(.regular) }
    }

    @objc private func mainWindowDidBecomeKey(_ notification: Notification) { restoreDockIcon() }

    @objc private func mainWindowWillClose(_ notification: Notification) {
        windowGeneration += 1
        let generation = windowGeneration
        // Let SwiftUI finish closing before changing policy. A reopen in the
        // meantime invalidates this pending change, including reused windows.
        DispatchQueue.main.async { [weak self] in
            guard let self, !self.terminating, self.windowGeneration == generation else { return }
            NSApp.setActivationPolicy(.accessory)
        }
    }

    func showMainWindow() {
        guard !terminating else { return }
        restoreDockIcon()
        if let mainWindow, mainWindow.isVisible || mainWindow.isMiniaturized {
            if mainWindow.isMiniaturized { mainWindow.deminiaturize(nil) }
            mainWindow.makeKeyAndOrderFront(nil)
        } else {
            reopenMainWindow?()
        }
        NSApp.activate(ignoringOtherApps: true)
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard !terminating, let model else { return .terminateNow }
        terminating = true
        Task { await model.quit(); sender.reply(toApplicationShouldTerminate: true) }
        return .terminateLater
    }
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows: Bool) -> Bool {
        showMainWindow()
        return false
    }
}

// Observe the actual SwiftUI main window without replacing its delegate.
struct MainWindowObserver: NSViewRepresentable {
    let delegate: AppDelegate
    func makeNSView(context: Context) -> WindowView {
        let view = WindowView()
        view.onWindow = { [weak delegate] in delegate?.observeMainWindow($0) }
        return view
    }
    func updateNSView(_ nsView: WindowView, context: Context) {}

    final class WindowView: NSView {
        var onWindow: ((NSWindow) -> Void)?
        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            if let window { onWindow?(window) }
        }
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
        .commands { DownloadCommands(model: model, delegate: delegate) }
        MenuBarExtra("DSL Q", systemImage: "arrow.down.circle") { MenuContent(model: model, delegate: delegate) }
    }
}

struct DownloadCommands: Commands {
    @ObservedObject var model: AppModel
    let delegate: AppDelegate
    var body: some Commands {
        CommandGroup(replacing: .newItem) { Button("新建下载") { model.windows.newDownload() }.keyboardShortcut("n") }
        CommandGroup(replacing: .appSettings) { Button("设置…") { delegate.showMainWindow(); model.showSettings = true }.keyboardShortcut(",") }
    }
}

struct MenuContent: View {
    @ObservedObject var model: AppModel
    let delegate: AppDelegate
    var body: some View {
        Text("\(model.activeCount) 个任务正在下载 · \(bytes(Int64(model.totalSpeed)))/s")
        Button("打开 DSL Q") { delegate.showMainWindow() }
        Button("新建下载…") { model.windows.newDownload() }
        Divider()
        Button("全部暂停") { for task in model.tasks where task.isActive || task.state == "queued" { model.action(task, "pause") } }
        Button("继续已暂停任务") { for task in model.tasks where task.state == "paused" { model.action(task, "resume") } }
        Toggle("下载时防止自动休眠", isOn: Binding(get: { model.settings.preventIdleSleepWhileDownloading }, set: model.toggleSleep))
        PowerStatus(power: model.power, enabled: model.settings.preventIdleSleepWhileDownloading)
        Divider()
        Button("设置…") { delegate.showMainWindow(); model.showSettings = true }
        Button("退出 DSL Q") { NSApp.terminate(nil) }.keyboardShortcut("q")
    }
}
