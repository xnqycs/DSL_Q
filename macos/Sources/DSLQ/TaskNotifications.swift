import Foundation
import UserNotifications
import SwiftUI

struct DownloadNotice: Equatable {
    let taskID: String
    let runKey: String
    let filename: String
    let phase: String
    var identifier: String { "download:\(runKey):\(phase)" }
    var title: String { ["started": "下载开始", "completed": "下载完成", "failed": "下载失败", "cancelled": "下载已取消"][phase] ?? "下载状态" }
}

struct DownloadNoticeTracker {
    private var initialized = false
    private var starts = Set<String>()
    private var endings = Set<String>()
    mutating func update(_ tasks: [DownloadTask]) -> [DownloadNotice] {
        defer { initialized = true }
        var result: [DownloadNotice] = []
        for task in tasks {
            guard task.started == true else { continue }
            if starts.insert(task.runKey).inserted && initialized {
                result.append(DownloadNotice(taskID: task.id, runKey: task.runKey, filename: task.name, phase: "started"))
            }
            if task.terminal && endings.insert(task.runKey).inserted && initialized {
                result.append(DownloadNotice(taskID: task.id, runKey: task.runKey, filename: task.name, phase: task.state))
            }
        }
        return result
    }
}

enum NotificationPermission { case notDetermined, denied, authorized }
@MainActor protocol DownloadNotificationService {
    func permission() async -> NotificationPermission
    func request() async -> NotificationPermission
    func deliver(_ notice: DownloadNotice) async throws
}
@MainActor final class SystemDownloadNotifications: DownloadNotificationService {
    let center = UNUserNotificationCenter.current()
    func permission() async -> NotificationPermission {
        let settings = await center.notificationSettings()
        switch settings.authorizationStatus {
        case .notDetermined: return .notDetermined
        case .authorized, .provisional: return .authorized
        default: return .denied
        }
    }
    func request() async -> NotificationPermission {
        _ = try? await center.requestAuthorization(options: [.alert, .sound])
        return await permission()
    }
    func deliver(_ notice: DownloadNotice) async throws {
        let content = UNMutableNotificationContent()
        content.title = notice.title; content.body = notice.filename; content.sound = .default
        content.userInfo = ["taskID": notice.taskID]
        try await center.add(UNNotificationRequest(identifier: notice.identifier, content: content, trigger: nil))
        if ProcessInfo.processInfo.arguments.contains("--integration-test") {
            try? await Task.sleep(nanoseconds: 300_000_000)
            let delivered = await center.deliveredNotifications()
            print("Notification check: \(notice.phase) \(notice.taskID) delivered=\(delivered.contains { $0.request.identifier == notice.identifier })")
            fflush(stdout)
        }
    }
}

@MainActor final class TaskNotifications: NSObject, ObservableObject, UNUserNotificationCenterDelegate {
    @Published var permission: NotificationPermission = .notDetermined
    @Published var deliveryError: String?
    var onOpenTask: ((String) -> Void)?
    private var tracker = DownloadNoticeTracker()
    private let service: DownloadNotificationService
    private var authorization: Task<Void, Never>?
    private var delivery: Task<Void, Never>?
    init(service: DownloadNotificationService? = nil) {
        let selected = service ?? SystemDownloadNotifications()
        self.service = selected
        super.init()
        if let system = selected as? SystemDownloadNotifications { system.center.delegate = self }
    }
    func refresh() { Task { permission = await service.permission() } }
    func requestIfNeeded() {
        guard authorization == nil else { return }
        authorization = Task {
            permission = await service.permission()
            if permission == .notDetermined { permission = await service.request() }
            authorization = nil
        }
    }
    func update(_ tasks: [DownloadTask], settings: DownloadSettings) {
        let events = tracker.update(tasks).filter { $0.phase == "started" ? settings.notifyOnStart : settings.notifyOnFinish }
        guard !events.isEmpty else { return }
        let previous = delivery
        delivery = Task {
            await previous?.value
            await authorization?.value
            permission = await service.permission()
            guard permission == .authorized else { return }
            for event in events {
                do { try await service.deliver(event); deliveryError = nil }
                catch { deliveryError = "通知未能投递，请检查系统通知设置。" }
            }
        }
    }
    // Also used by deterministic permission and fast-file checks.
    func flush() async { await authorization?.value; await delivery?.value }
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification, withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler([.banner, .list, .sound])
    }
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse, withCompletionHandler completionHandler: @escaping () -> Void) {
        let id = response.notification.request.content.userInfo["taskID"] as? String
        Task { @MainActor in if let id { self.onOpenTask?(id) }; completionHandler() }
    }
}
