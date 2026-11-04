import Foundation

@MainActor final class FakeNotifications: DownloadNotificationService {
    var status: NotificationPermission = .notDetermined
    var grant: NotificationPermission = .authorized
    var requests = 0
    var delivered: [DownloadNotice] = []
    func permission() async -> NotificationPermission { status }
    func request() async -> NotificationPermission { requests += 1; status = grant; return status }
    func deliver(_ notice: DownloadNotice) async throws { delivered.append(notice) }
}

@main struct NotificationChecks {
    static func task(_ id: String = "file", run: String = "run-1", state: String = "queued", started: Bool = false) -> DownloadTask {
        let object: [String: Any] = ["id":id,"name":"archive.zip","source":"https://example.test/file","path":"/tmp/archive.zip","state":state,"total":0,"received":0,"speed":0,"connections":0,"activeConnections":0,"chunks":[],"error":"","createdAt":"2026-11-04T00:00:00Z","resumable":false,"runId":run,"started":started]
        return try! JSONDecoder().decode(DownloadTask.self,from:JSONSerialization.data(withJSONObject:object))
    }
    @MainActor static func main() async throws {
        var tracker = DownloadNoticeTracker()
        precondition(tracker.update([task("history",state:"completed",started:true)]).isEmpty)
        precondition(tracker.update([task()]).isEmpty)
        let fast = task(state:"completed",started:true)
        precondition(tracker.update([fast]).map(\.phase) == ["started","completed"])
        precondition(tracker.update([fast]).isEmpty)
        let second = task("second",state:"downloading",started:true)
        precondition(tracker.update([second]).map(\.phase) == ["started"])
        precondition(tracker.update([task("second",state:"paused",started:true)]).isEmpty)
        precondition(tracker.update([second]).isEmpty)
        precondition(tracker.update([task("second",state:"failed",started:true)]).map(\.phase) == ["failed"])
        precondition(tracker.update([task("second",run:"run-2",state:"completed",started:true)]).map(\.phase) == ["started","completed"])
        precondition(tracker.update([task("pending",state:"cancelled")]).isEmpty)
        var restarted = DownloadNoticeTracker()
        precondition(restarted.update([second,fast]).isEmpty)
        precondition(restarted.update([task("second",state:"completed",started:true),fast]).map(\.phase) == ["completed"])

        let authorized = FakeNotifications(), allowed = TaskNotifications(service: authorized)
        let settings = DownloadSettings()
        allowed.update([],settings:settings); allowed.requestIfNeeded(); allowed.update([fast],settings:settings)
        await allowed.flush()
        precondition(authorized.requests == 1 && authorized.delivered.map(\.phase) == ["started","completed"])
        allowed.update([fast],settings:settings); await allowed.flush(); precondition(authorized.delivered.count == 2)
        precondition(authorized.delivered.allSatisfy { !$0.identifier.contains("https") && $0.filename == "archive.zip" })

        let rejected = FakeNotifications(); rejected.grant = .denied
        let denied = TaskNotifications(service: rejected)
        denied.update([],settings:settings); denied.requestIfNeeded(); denied.update([fast],settings:settings)
        await denied.flush(); denied.requestIfNeeded(); await denied.flush()
        precondition(rejected.requests == 1 && rejected.delivered.isEmpty)

        let mutedService = FakeNotifications(); mutedService.status = .authorized
        let muted = TaskNotifications(service: mutedService)
        var disabled = settings; disabled.notifyOnStart = false; disabled.notifyOnFinish = false
        muted.update([],settings:disabled); muted.update([fast],settings:disabled); await muted.flush()
        muted.update([fast],settings:settings); await muted.flush(); precondition(mutedService.delivered.isEmpty)
        print("Notification checks passed: queued/fast files, history, pause/resume, retry identity, cancellation, permissions and deduplication")
    }
}
