// swift-tools-version: 6.0
import PackageDescription

let package = Package(
  name: "window",
  platforms: [.macOS(.v15)],
  targets: [
    .target(name: "WindowCore"),
    .executableTarget(name: "window", dependencies: ["WindowCore"]),
    .testTarget(name: "WindowCoreTests", dependencies: ["WindowCore"]),
  ]
)
