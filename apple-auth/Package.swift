// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "VirfieldAppleAuth",
    platforms: [.macOS(.v13)],
    dependencies: [
        .package(url: "https://github.com/XcodesOrg/XcodesLoginKit", revision: "929f9aac3140caf7b64cbb5385f4f645c5f9913d")
    ],
    targets: [
        .executableTarget(
            name: "virfield-apple-auth",
            dependencies: [.product(name: "XcodesLoginKit", package: "XcodesLoginKit")]
        )
    ]
)
