import Combine
import Foundation

@MainActor private final class SnapshotNotifications: DownloadNotificationService {
    var delivered: [DownloadNotice] = []
    func permission() async -> NotificationPermission { .authorized }
    func request() async -> NotificationPermission { .authorized }
    func deliver(_ notice: DownloadNotice) async throws { delivered.append(notice) }
}

@main struct SnapshotChecks {
    @MainActor static func main() async throws {
        try await identicalSnapshots()
        try await notificationBaseline()
        print("Snapshot checks passed: idle publication, task/chunk/settings changes, protocol validation, empty and historical notification baselines")
    }

    private static func task(state: String = "downloading", run: String = "run", received: Int = 10, done: Bool = false) throws -> DownloadTask {
        let object: [String: Any] = ["id": "file", "runId": run, "started": true, "name": "file.zip", "source": "https://example.test/file",
            "path": "/tmp/file.zip", "state": state, "total": 100, "received": received, "speed": 0, "connections": 1,
            "activeConnections": 0, "chunks": [["start": 0, "end": 99, "done": done]], "error": "", "createdAt": "2026-11-06T00:00:00Z", "resumable": true]
        return try JSONDecoder().decode(DownloadTask.self, from: JSONSerialization.data(withJSONObject: object))
    }

    @MainActor static func identicalSnapshots() async throws {
        let model = AppModel(notifications: TaskNotifications(service: SnapshotNotifications()))
        var taskChanges = 0, settingsChanges = 0, powerChanges = 0
        let subscriptions = [model.$tasks.dropFirst().sink { _ in taskChanges += 1 },
            model.$settings.dropFirst().sink { _ in settingsChanges += 1 },
            model.power.objectWillChange.sink { powerChanges += 1 }]
        defer { withExtendedLifetime(subscriptions) {} }
        let empty = EngineSnapshot(tasks: [], settings: model.settings, activeCount: 0, protocol: 1)
        for _ in 0..<12 { try model.apply(empty) }
        precondition(taskChanges == 0 && settingsChanges == 0 && powerChanges == 0)

        var settings = model.settings
        settings.connections = 4
        try model.apply(EngineSnapshot(tasks: [], settings: settings, activeCount: 0, protocol: 1))
        precondition(settingsChanges == 1 && taskChanges == 0)
        for current in [try task(), try task(received: 50), try task(received: 50, done: true), try task(state: "paused"), try task(state: "cancelled")] {
            let snapshot = EngineSnapshot(tasks: [current], settings: settings, activeCount: current.isActive ? 1 : 0, protocol: 1)
            let before = taskChanges
            for _ in 0..<12 { try model.apply(snapshot) }
            precondition(taskChanges == before + 1)
        }
        precondition(settingsChanges == 1 && powerChanges == 0)
        do {
            try model.apply(EngineSnapshot(tasks: [], settings: settings, activeCount: 0, protocol: 2))
            preconditionFailure("incompatible protocol accepted")
        } catch { precondition(model.tasks.count == 1) }
        await model.notifications.flush()
    }

    @MainActor static func notificationBaseline() async throws {
        let service = SnapshotNotifications()
        let model = AppModel(notifications: TaskNotifications(service: service))
        let empty = EngineSnapshot(tasks: [], settings: model.settings, activeCount: 0, protocol: 1)
        try model.apply(empty)
        let complete = EngineSnapshot(tasks: [try task(state: "completed", received: 100, done: true)], settings: model.settings, activeCount: 0, protocol: 1)
        try model.apply(complete); try model.apply(complete)
        await model.notifications.flush()
        precondition(service.delivered.map(\.phase) == ["started", "completed"])

        // A first snapshot containing historical tasks must remain silent.
        let historyService = SnapshotNotifications()
        let notifications = TaskNotifications(service: historyService)
        let history = AppModel(notifications: notifications)
        try history.apply(complete)
        await notifications.flush()
        precondition(historyService.delivered.isEmpty)
    }
}
