import CoreGraphics
import Foundation
import WindowCore

let request: Request
switch parse(Array(CommandLine.arguments.dropFirst())) {
case .success(let parsed): request = parsed
case .failure(let error):
  FileHandle.standardError.write(Data(error.message.utf8))
  exit(2)
}

func onScreenWindows() -> [WindowInfo] {
  let list =
    CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]]
    ?? []
  return list.filter { ($0[kCGWindowLayer as String] as? Int) == 0 }.map { w in
    let bounds = (w[kCGWindowBounds as String] as? [String: Any]) ?? [:]
    return WindowInfo(
      id: (w[kCGWindowNumber as String] as? Int) ?? -1,
      pid: (w[kCGWindowOwnerPID as String] as? Int) ?? -1,
      owner: (w[kCGWindowOwnerName as String] as? String) ?? "",
      title: (w[kCGWindowName as String] as? String) ?? "",
      width: (bounds["Width"] as? Int) ?? 0,
      height: (bounds["Height"] as? Int) ?? 0)
  }
}

func chosen(_ result: Result<WindowInfo, SelectionError>) -> WindowInfo {
  switch result {
  case .success(let window): return window
  case .failure(let error): fail(error.message)
  }
}

switch request {
case .help:
  print(usage, terminator: "")
case .list(let pid):
  switch owned(by: pid, in: onScreenWindows()) {
  case .success(let windows): print(listing(windows), terminator: "")
  case .failure(let error): fail(error.message)
  }
case .shot(let pid, let out, let window):
  let target = chosen(select(pid: pid, window: window, in: onScreenWindows()))
  // `screencapture -l` takes a window id and nothing on macOS prints one, which is the gap
  // this fills; it gives the window's ordinary buttons where a stream draws the sharing pill.
  let capture = Process()
  capture.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
  capture.arguments = ["-x", "-o", "-l", String(target.id), out]
  do { try capture.run() } catch { fail("screencapture: \(error.localizedDescription)") }
  capture.waitUntilExit()
  if capture.terminationStatus != 0 { exit(1) }
  print(out)
case .record(let pid, let window, let recording):
  let target = chosen(select(pid: pid, window: window, in: onScreenWindows()))
  // SCStream aborts in a process that has never opened its window-server connection.
  _ = CGMainDisplayID()
  let recorder = Recorder(windowID: CGWindowID(target.id), recording: recording)
  recorder.start()
  // Not dispatchMain(), which drains the main queue off the main thread: ScreenCaptureKit
  // builds its menu bar recording indicator there, and AppKit aborts off the main thread.
  RunLoop.main.run()
}
