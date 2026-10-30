import CoreGraphics
import Foundation
import ScreenCaptureKit
import WindowCore

func say(_ message: String) {
  FileHandle.standardError.write(Data("window: \(message)\n".utf8))
}

func fail(_ message: String) -> Never {
  say(message)
  exit(1)
}

/// Records one window through ScreenCaptureKit's desktop-independent window filter, which
/// takes the window's own pixels while others cover it and raises nothing.
@MainActor
final class Recorder: NSObject, SCRecordingOutputDelegate, SCStreamDelegate {
  let windowID: CGWindowID
  let recording: Recording
  let out: URL
  var stream: SCStream?
  var child: Process?
  var stopping = false
  var status: Int32 = 0
  var signals: [DispatchSourceSignal] = []

  init(windowID: CGWindowID, recording: Recording) {
    self.windowID = windowID
    self.recording = recording
    out = URL(fileURLWithPath: recording.out)
  }

  func start() {
    for sig in [SIGINT, SIGTERM] {
      signal(sig, SIG_IGN)
      let source = DispatchSource.makeSignalSource(signal: sig, queue: .main)
      source.setEventHandler { [self] in MainActor.assumeIsolated { stop(signal: sig) } }
      source.resume()
      signals.append(source)
    }
    Task {
      do {
        try await capture()
      } catch {
        fail("starting the recording: \(error.localizedDescription)")
      }
    }
  }

  func capture() async throws {
    let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true)
    guard let window = content.windows.first(where: { $0.windowID == windowID }) else {
      fail("ScreenCaptureKit does not offer window \(windowID)")
    }
    let filter = SCContentFilter(desktopIndependentWindow: window)
    let scale = Double(filter.pointPixelScale)
    let size = videoSize(
      width: filter.contentRect.width * scale, height: filter.contentRect.height * scale, full: recording.full)
    let config = SCStreamConfiguration()
    config.width = size.width
    config.height = size.height
    config.minimumFrameInterval = CMTime(value: 1, timescale: 30)
    config.showsCursor = recording.showCursor
    config.showMouseClicks = recording.showCursor
    config.ignoreShadowsSingleWindow = true

    try? FileManager.default.removeItem(at: out)
    let outputConfig = SCRecordingOutputConfiguration()
    outputConfig.outputURL = out
    outputConfig.outputFileType = recording.container == .mp4 ? .mp4 : .mov
    outputConfig.videoCodecType = .h264
    let stream = SCStream(filter: filter, configuration: config, delegate: self)
    try stream.addRecordingOutput(SCRecordingOutput(configuration: outputConfig, delegate: self))
    self.stream = stream
    try await stream.startCapture()
    DispatchQueue.main.asyncAfter(deadline: .now() + recording.maxSeconds) { [self] in
      MainActor.assumeIsolated { stop() }
    }
  }

  nonisolated func recordingOutputDidStartRecording(_ output: SCRecordingOutput) {
    Task { @MainActor in started() }
  }

  nonisolated func recordingOutput(_ output: SCRecordingOutput, didFailWithError error: any Error) {
    let message = error.localizedDescription
    Task { @MainActor in
      child?.terminate()
      fail("the recording failed, as it does for a minimised window: \(message)")
    }
  }

  nonisolated func recordingOutputDidFinishRecording(_ output: SCRecordingOutput) {
    Task { @MainActor in
      print(out.path)
      exit(status)
    }
  }

  nonisolated func stream(_ stream: SCStream, didStopWithError error: any Error) {
    let message = error.localizedDescription
    Task { @MainActor in
      child?.terminate()
      fail("the window's stream stopped, as it does when the window closes: \(message)")
    }
  }

  func started() {
    let startMS = Int64(Date().timeIntervalSince1970 * 1000)
    say("recording window \(windowID) to \(out.path) from \(startMS) ms")
    guard !recording.command.isEmpty, !stopping else { return }
    let process = Process()
    process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
    process.arguments = recording.command
    var env = ProcessInfo.processInfo.environment
    env["WINDOW_RECORD_START_MS"] = String(startMS)
    process.environment = env
    process.terminationHandler = { [self] process in
      let code = exitStatus(
        signalled: process.terminationReason == .uncaughtSignal, status: process.terminationStatus)
      Task { @MainActor in
        status = code
        stop()
      }
    }
    do {
      try process.run()
    } catch {
      say("running \(recording.command[0]): \(error.localizedDescription)")
      status = 127
      stop()
      return
    }
    child = process
  }

  func stop(signal sig: Int32? = nil) {
    if let sig, let child, child.isRunning { kill(child.processIdentifier, sig) }
    guard !stopping, let stream else { return }
    stopping = true
    if let child, child.isRunning { child.terminate() }
    Task {
      do {
        try await stream.stopCapture()
      } catch {
        fail("stopping: \(error.localizedDescription)")
      }
    }
  }
}
