import Testing
@testable import lume

private enum RegistryPipelineTestError: Error { case uploadFailed }

@Test("OCI push observes upload pipeline errors before publishing")
func registryUploadPipelinePropagatesFailure() async throws {
    do {
        try await withThrowingTaskGroup(of: Void.self) { pipeline in
            pipeline.addTask { throw RegistryPipelineTestError.uploadFailed }
            try await awaitAllOCIUploadPipelines(&pipeline)
        }
        Issue.record("An upload failure was ignored")
    } catch RegistryPipelineTestError.uploadFailed {}
}

@Test("OCI push rejects a missing disk chunk instead of publishing a partial manifest")
func registryChunkCollectorRequiresEveryPart() async throws {
    let collector = ChunkCollector(count: 3)
    let descriptor = DiskChunkDescriptor(
        partNumber: 0, compressedDigest: "sha256:example", compressedSize: 1,
        uncompressedDigest: "sha256:example", uncompressedSize: 4, diskOffset: 0)
    await collector.set(index: 0, desc: descriptor)
    await collector.set(index: 2, desc: descriptor)
    do {
        _ = try await collector.getAll()
        Issue.record("An incomplete disk was accepted")
    } catch PushError.missingPart(let index) {
        #expect(index == 1)
    }
    await collector.set(index: 1, desc: descriptor)
    #expect(try await collector.getAll().count == 3)
}
