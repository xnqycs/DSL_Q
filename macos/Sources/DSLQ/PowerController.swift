import Foundation
import IOKit.pwr_mgt

protocol PowerAssertions {
    func acquire() throws -> IOPMAssertionID
    func release(_ id: IOPMAssertionID)
}

struct SystemPowerAssertions: PowerAssertions {
    func acquire() throws -> IOPMAssertionID {
        var id: IOPMAssertionID = 0
        let result = IOPMAssertionCreateWithName(kIOPMAssertionTypePreventUserIdleSystemSleep as CFString,
            IOPMAssertionLevel(kIOPMAssertionLevelOn), "DSL Q active downloads" as CFString, &id)
        guard result == kIOReturnSuccess else { throw DSLQError.message("电源管理请求失败（\(result)）") }
        return id
    }
    func release(_ id: IOPMAssertionID) { IOPMAssertionRelease(id) }
}

@MainActor
final class PowerController: ObservableObject {
    @Published private(set) var holding = false
    @Published private(set) var failure: String?
    private var assertion: IOPMAssertionID?
    private let provider: PowerAssertions
    private var previouslyWanted = false
    init(provider: PowerAssertions = SystemPowerAssertions()) { self.provider = provider }

    func update(enabled: Bool, activeCount: Int, connected: Bool) {
        let wanted = enabled && connected && activeCount > 0
        if !wanted {
            release()
            previouslyWanted = false
            if failure != nil { failure = nil }
        } else if !previouslyWanted {
            previouslyWanted = true
            do {
                assertion = try provider.acquire()
                if !holding { holding = true }
                if failure != nil { failure = nil }
            } catch {
                if holding { holding = false }
                if failure != error.localizedDescription { failure = error.localizedDescription }
            }
        }
    }
    func release() {
        if let id = assertion { provider.release(id); assertion = nil }
        if holding { holding = false }
        previouslyWanted = false
    }
}
