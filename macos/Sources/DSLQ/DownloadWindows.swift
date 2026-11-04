import AppKit
import SwiftUI

@MainActor
private final class DownloadWindowController: NSWindowController, NSWindowDelegate {
    var requestClose: (() -> Void)?
    var didClose: (() -> Void)?
    var allowClose = false
    init<Content: View>(title: String, size: NSSize, content: Content) {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
        window.title = title; window.isReleasedWhenClosed = false
        window.tabbingMode = .disallowed
        window.contentView = NSHostingView(rootView: content)
        window.contentMinSize = NSSize(width: 520, height: size.height)
        super.init(window: window); window.delegate = self; window.center()
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) is not supported") }
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        if !allowClose, let requestClose { requestClose(); return false }
        return true
    }
    func windowWillClose(_ notification: Notification) { didClose?() }
    func dismiss() { allowClose = true; close() }
}

@MainActor
final class DownloadWindows {
    private unowned let model: AppModel
    private var queue: [DownloadConfirmation] = []
    private var browserIDs = Set<String>()
    private var confirmation: DownloadWindowController?
    private var current: DownloadConfirmation?
    private var progress: [String: DownloadWindowController] = [:]
    private var cascade = NSPoint.zero
    private var quitting = false
    init(model: AppModel) { self.model = model }

    func newDownload() {
        guard model.connected else { return }
        if let current, current.taskID == nil { confirmation?.window?.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true); return }
        queue.append(DownloadConfirmation(settings: model.settings)); showNext()
        confirmation?.window?.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true)
    }
    func sync(_ tasks: [DownloadTask]) {
        guard !quitting else { return }
        for task in tasks where task.awaitingConfirmation && !browserIDs.contains(task.id) {
            browserIDs.insert(task.id); queue.append(DownloadConfirmation(task: task, settings: model.settings))
        }
        let awaiting = Set(tasks.filter(\.awaitingConfirmation).map(\.id))
        // A task may also be cancelled from the library while its confirmation
        // window is open. Do not leave a stale dialog blocking the next task.
        if let session = current, let id = session.taskID, !session.working, !awaiting.contains(id) {
            session.previewGeneration += 1
            current = nil; let old = confirmation; confirmation = nil; old?.dismiss()
        }
        queue.removeAll { session in session.taskID.map { !awaiting.contains($0) } ?? false }
        showNext()
    }
    func showConfirmation(_ id: String) {
        guard let task = model.tasks.first(where: { $0.id == id }), task.awaitingConfirmation else { return }
        if current?.taskID == id { confirmation?.window?.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true); return }
        sync(model.tasks)
        if let index = queue.firstIndex(where: { $0.taskID == id }) { let session = queue.remove(at: index); queue.insert(session, at: 0) }
        showNext()
    }
    private func showNext() {
        guard !quitting, current == nil, !queue.isEmpty else { return }
        let session = queue.removeFirst(); current = session
        let controller = DownloadWindowController(title: "下载文件信息", size: NSSize(width: 640, height: session.taskID == nil ? 650 : 520), content: DownloadConfirmationView(session: session, model: model))
        controller.requestClose = { [weak self, weak session] in if let session { self?.cancel(session) } }
        confirmation = controller
        controller.showWindow(nil); controller.window?.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true)
    }
    func start(_ session: DownloadConfirmation) {
        guard current?.id == session.id, !session.working, model.connected else { return }
        guard !session.filename.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { session.error = "请输入文件名"; return }
        if session.taskID == nil && !session.validURL { session.error = "请输入有效的 HTTP / HTTPS 文件链接"; return }
        session.working = true; session.error = nil
        if model.settings.notifyOnStart || model.settings.notifyOnFinish { model.notifications.requestIfNeeded() }
        Task {
            do {
                let task: DownloadTask
                if let id = session.taskID {
                    task = try await model.taskRequest("tasks.confirm", ["id": id, "filename": session.filename, "directory": session.directory, "connections": session.connections])
                } else {
                    task = try await model.taskRequest("tasks.add", ["url": session.currentURL, "filename": session.filename, "directory": session.directory, "connections": session.connections])
                }
                openProgress(task.id)
                complete(session)
            } catch { session.error = error.localizedDescription; session.working = false }
        }
    }
    func cancel(_ session: DownloadConfirmation) {
        guard current?.id == session.id, !session.working else { return }
        session.working = true; session.error = nil
        Task {
            do {
                if let id = session.taskID { _ = try await model.request("tasks.action", ["id": id, "action": "cancel"]) }
                complete(session)
            } catch { session.error = error.localizedDescription; session.working = false }
        }
    }
    private func complete(_ session: DownloadConfirmation) {
        guard current?.id == session.id else { return }
        session.previewGeneration += 1
        if session.taskID == nil {
            let remaining = session.urls.dropFirst().map { DownloadConfirmation(url: $0, settings: model.settings) }
            // Preserve directory/concurrency choices within a batch only.
            for next in remaining { next.directory = session.directory; next.connections = session.connections }
            queue.insert(contentsOf: remaining, at: 0)
        }
        current = nil; let old = confirmation; confirmation = nil; old?.dismiss()
        DispatchQueue.main.async { [weak self] in self?.showNext() }
    }
    func openProgress(_ id: String) {
        if let task = model.tasks.first(where: { $0.id == id }), task.awaitingConfirmation { showConfirmation(id); return }
        if let existing = progress[id] { existing.window?.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true); return }
        let title = model.tasks.first(where: { $0.id == id })?.name ?? "下载进度"
        let controller = DownloadWindowController(title: title, size: NSSize(width: 620, height: 570), content: DownloadProgressView(taskID: id, model: model))
        controller.didClose = { [weak self] in self?.progress.removeValue(forKey: id) }
        if cascade != .zero { cascade = controller.window?.cascadeTopLeft(from: cascade) ?? .zero }
        else { cascade = controller.window?.cascadeTopLeft(from: .zero) ?? .zero }
        progress[id] = controller
        controller.showWindow(nil); controller.window?.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true)
    }
    func prepareForQuit() {
        quitting = true; current?.previewGeneration += 1
        confirmation?.dismiss(); confirmation = nil; current = nil
        let windows = Array(progress.values); progress.removeAll(); for window in windows { window.dismiss() }
        queue.removeAll()
    }
}
