public struct WindowInfo: Sendable, Equatable {
  public var id: Int
  public var pid: Int
  public var owner: String
  public var title: String
  public var width: Int
  public var height: Int

  public init(id: Int, pid: Int, owner: String, title: String, width: Int, height: Int) {
    self.id = id
    self.pid = pid
    self.owner = owner
    self.title = title
    self.width = width
    self.height = height
  }
}

public struct SelectionError: Error, Equatable {
  public var message: String
}

/// The on-screen windows a pid owns, front to back as the window list gives them, or why
/// there are none.
public func owned(by pid: Int, in windows: [WindowInfo]) -> Result<[WindowInfo], SelectionError> {
  let owned = windows.filter { $0.pid == pid }
  if !owned.isEmpty { return .success(owned) }
  let owners = Set(windows.map { "\($0.owner) (pid \($0.pid))" }).sorted()
  let others = owners.isEmpty ? "no app has a window on screen" : "on screen: \(owners.joined(separator: ", "))"
  return .failure(SelectionError(message: "no on-screen window owned by pid \(pid); \(others)"))
}

/// The one window a capture is of. A pid with several is refused rather than its frontmost
/// taken, since which one is in front is not the caller's to know.
public func select(pid: Int, window wanted: Int?, in windows: [WindowInfo]) -> Result<WindowInfo, SelectionError> {
  owned(by: pid, in: windows).flatMap { owned in
    let match = wanted.map { id in owned.filter { $0.id == id } } ?? owned
    if match.count == 1 { return .success(match[0]) }
    let listing = owned.map { "\($0.id) \"\($0.title)\"" }.joined(separator: ", ")
    if let id = wanted {
      return .failure(SelectionError(message: "pid \(pid) has no on-screen window \(id); its windows: \(listing)"))
    }
    return .failure(
      SelectionError(message: "pid \(pid) has \(owned.count) on-screen windows; pick one with --window <id>: \(listing)"))
  }
}

public func listing(_ windows: [WindowInfo]) -> String {
  windows.map { "\($0.id) \($0.width)x\($0.height) \"\($0.title)\"\n" }.joined()
}
