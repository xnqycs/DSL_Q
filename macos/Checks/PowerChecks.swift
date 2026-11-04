import Foundation
import IOKit.pwr_mgt

final class FakeAssertions: PowerAssertions {
    var acquisitions = 0; var releases = 0; var fail = false
    func acquire() throws -> IOPMAssertionID { acquisitions += 1; if fail { throw DSLQError.message("denied") }; return 42 }
    func release(_ id: IOPMAssertionID) { releases += 1 }
}

@main
struct PowerChecks {
    @MainActor static func main() throws {
        let fake = FakeAssertions()
        let controller = PowerController(provider: fake)
        controller.update(enabled: false, activeCount: 1, connected: true)
        precondition(!controller.holding && fake.acquisitions == 0)
        controller.update(enabled: true, activeCount: 1, connected: true)
        controller.update(enabled: true, activeCount: 3, connected: true)
        precondition(controller.holding && fake.acquisitions == 1)
        controller.update(enabled: true, activeCount: 0, connected: true)
        precondition(!controller.holding && fake.releases == 1)
        controller.update(enabled: true, activeCount: 1, connected: true)
        controller.update(enabled: true, activeCount: 1, connected: false)
        precondition(!controller.holding && fake.releases == 2)
        controller.update(enabled: true, activeCount: 1, connected: true)
        controller.update(enabled: false, activeCount: 1, connected: true)
        precondition(fake.acquisitions == 3 && fake.releases == 3)
        fake.fail = true
        controller.update(enabled: true, activeCount: 1, connected: true)
        controller.update(enabled: true, activeCount: 2, connected: true)
        precondition(!controller.holding && controller.failure != nil && fake.acquisitions == 4)
        controller.update(enabled: false, activeCount: 0, connected: true); fake.fail = false
        controller.update(enabled: true, activeCount: 1, connected: true)
        precondition(controller.holding && controller.failure == nil); controller.release()
        precondition(fake.acquisitions == fake.releases + 1) // one failed acquisition

        let real = PowerController()
        real.update(enabled: true, activeCount: 1, connected: true)
        precondition(real.holding, real.failure ?? "real assertion not acquired")
        RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        let held = try pmset()
        print(held.split(separator: "\n").filter { $0.contains("pid \(ProcessInfo.processInfo.processIdentifier)(") }.joined(separator: "\n"))
        precondition(held.contains("pid \(ProcessInfo.processInfo.processIdentifier)("))
        real.release()
        RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        let released = try pmset()
        precondition(!released.contains("pid \(ProcessInfo.processInfo.processIdentifier)("))
        print("Power checks passed: lifecycle, failure recovery, real IOKit acquisition/release verified with pmset")
    }
    static func pmset() throws -> String {
        let p = Process(), out = Pipe(); p.executableURL = URL(fileURLWithPath:"/usr/bin/pmset"); p.arguments = ["-g","assertions"]; p.standardOutput = out
        try p.run(); let data = out.fileHandleForReading.readDataToEndOfFile(); p.waitUntilExit()
        return String(decoding: data, as: UTF8.self)
    }
}
