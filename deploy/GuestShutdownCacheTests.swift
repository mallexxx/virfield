import Foundation
import Testing
@testable import lume

private enum GuestShutdownTestError: Error { case failed }

@MainActor
@Test("setup shutdown accepts SSH disconnect only after VM lifecycle completion")
func setupShutdownRequiresLifecycleCompletion() async throws {
    var requests = 0
    var observations = 0
    try await UnattendedInstaller.waitForGuestShutdown(request: {
        requests += 1
        throw GuestShutdownTestError.failed
    }, completed: {
        observations += 1
        return observations == 3
    }, timeout: 1, pollNanoseconds: 1_000_000)
    #expect(requests == 1)
    #expect(observations == 3)
    do {
        try await UnattendedInstaller.waitForGuestShutdown(request: {
            requests += 1
        }, completed: { false }, timeout: 0)
        Issue.record("SSH success without completed VM shutdown was accepted")
    } catch {}
    #expect(requests == 2)
}

@MainActor
private final class CompletedGuestVM: MockVM {
    var fail = false
    override func run(
        displayMode: DisplayMode = .vnc, sharedDirectories: [SharedDirectory], mount: Path?,
        vncPort: Int = 0, vncPassword: String? = nil, recoveryMode: Bool = false,
        usbMassStoragePaths: [Path]? = nil, additionalDiskPaths: [Path]? = nil,
        networkMode: NetworkMode? = nil, clipboard: Bool = false
    ) async throws {
        #expect(SharedVM.shared.getVM(name: vmDirContext.name) === self)
        if fail { throw GuestShutdownTestError.failed }
        // Returning represents guestDidStop and completed session cleanup.
    }
}

@MainActor
private final class CompletedGuestFactory: VMFactory {
    var fail = false
    func createVM(vmDirContext: VMDirContext, imageLoader: ImageLoader?) throws -> VM {
        let vm = CompletedGuestVM(
            vmDirContext: vmDirContext,
            virtualizationServiceFactory: { _ in MockVMVirtualizationService() },
            vncServiceFactory: { MockVNCService(vmDirectory: $0) }
        )
        vm.fail = fail
        return vm
    }
}

@MainActor
@Test("guest shutdown releases the running cache on success and failure")
func guestShutdownReleasesRunningCache() async throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: root) }
    let oldConfig = ProcessInfo.processInfo.environment["XDG_CONFIG_HOME"]
    setenv("XDG_CONFIG_HOME", root.appendingPathComponent("config").path, 1)
    defer {
        if let oldConfig { setenv("XDG_CONFIG_HOME", oldConfig, 1) }
        else { unsetenv("XDG_CONFIG_HOME") }
    }
    let settings = SettingsManager(fileManager: .default)
    try settings.setHomeDirectory(path: root.appendingPathComponent("vms").path)
    let home = Home(settingsManager: settings, fileManager: .default)
    let factory = CompletedGuestFactory()
    let controller = LumeController(home: home, vmFactory: factory)
    let vmDir = try home.getVMDirectory("guest-shutdown-cache")
    try FileManager.default.createDirectory(at: vmDir.dir.url, withIntermediateDirectories: true)
    try Data(repeating: 0, count: 1024).write(to: vmDir.diskPath.url)
    try Data(repeating: 0, count: 1024).write(to: vmDir.nvramPath.url)
    try vmDir.saveConfig(VMConfig(os: "macOS", cpuCount: 1, memorySize: 1024,
                                  diskSize: 1024, display: "1024x768"))
    for fail in [false, true] {
        factory.fail = fail
        do {
            try await controller.runVM(name: vmDir.name, noDisplay: true)
            #expect(!fail)
        } catch GuestShutdownTestError.failed {
            #expect(fail)
        }
        #expect(SharedVM.shared.getVM(name: vmDir.name) == nil)
        SharedVM.shared.removeVM(name: vmDir.name)
    }
}
