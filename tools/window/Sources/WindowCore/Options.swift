import Foundation

public let usage = """
  usage: window list <pid>
         window shot <pid> <out.png> [--window <id>]
         window record <pid> <out.mp4|out.mov> [--window <id>] [--max <seconds>] [--no-cursor] [--full] [-- <command> ...]
    pid is the app's main process; --window picks one of its windows by the id list prints.
    record stops on SIGINT or SIGTERM, when <command> exits, or after --max seconds (default 300).

  """

public enum Container: Sendable, Equatable {
  case mp4, mov
}

public struct Recording: Sendable, Equatable {
  public var out: String
  public var container: Container
  public var maxSeconds: Double = 300
  public var showCursor = true
  public var full = false
  public var command: [String] = []

  public init(
    out: String, container: Container, maxSeconds: Double = 300, showCursor: Bool = true, full: Bool = false,
    command: [String] = []
  ) {
    self.out = out
    self.container = container
    self.maxSeconds = maxSeconds
    self.showCursor = showCursor
    self.full = full
    self.command = command
  }
}

public enum Request: Sendable, Equatable {
  case help
  case list(pid: Int)
  case shot(pid: Int, out: String, window: Int?)
  case record(pid: Int, window: Int?, Recording)
}

public enum ParseError: Error, Equatable {
  case usage
  case container(String)

  public var message: String {
    switch self {
    case .usage: WindowCore.usage
    case .container(let out): "window: a recording is .mp4 or .mov, not \(out)\n"
    }
  }
}

public func parse(_ arguments: [String]) -> Result<Request, ParseError> {
  if arguments.first == "-h" || arguments.first == "--help" { return .success(.help) }
  // A process name or a title can belong to another session's copy of the app, so the
  // first argument is a pid and nothing else.
  guard arguments.count >= 2, ["list", "shot", "record"].contains(arguments[0]),
    let pid = Int(arguments[1]), pid > 0
  else { return .failure(.usage) }
  let mode = arguments[0]

  var args = Array(arguments.dropFirst(2))
  var command: [String] = []
  if mode == "record", let dash = args.firstIndex(of: "--") {
    command = Array(args[(dash + 1)...])
    args = Array(args[..<dash])
    if command.isEmpty { return .failure(.usage) }
  }

  var positional: [String] = []
  var window: Int?
  var maxSeconds = 300.0
  var showCursor = true
  var full = false
  var i = 0
  while i < args.count {
    let arg = args[i]
    func value() -> String? {
      i += 1
      return i < args.count ? args[i] : nil
    }
    switch arg {
    case "--window" where mode != "list":
      guard let id = value().flatMap({ Int($0) }), id > 0 else { return .failure(.usage) }
      window = id
    case "--max" where mode == "record":
      guard let seconds = value().flatMap({ Double($0) }), seconds > 0 else { return .failure(.usage) }
      maxSeconds = seconds
    case "--no-cursor" where mode == "record": showCursor = false
    case "--full" where mode == "record": full = true
    default:
      if arg.hasPrefix("-") { return .failure(.usage) }
      positional.append(arg)
    }
    i += 1
  }

  switch mode {
  case "list":
    return positional.isEmpty ? .success(.list(pid: pid)) : .failure(.usage)
  case "shot":
    guard positional.count == 1 else { return .failure(.usage) }
    return .success(.shot(pid: pid, out: positional[0], window: window))
  default:
    guard positional.count == 1 else { return .failure(.usage) }
    let out = positional[0]
    let container: Container
    switch (out as NSString).pathExtension {
    case "mp4": container = .mp4
    case "mov": container = .mov
    default: return .failure(.container(out))
    }
    return .success(
      .record(
        pid: pid, window: window,
        Recording(
          out: out, container: container, maxSeconds: maxSeconds, showCursor: showCursor, full: full,
          command: command)))
  }
}

/// The status a wrapped command's end is reported with, as a shell reports it.
public func exitStatus(signalled: Bool, status: Int32) -> Int32 {
  signalled ? 128 + status : status
}

/// The recording's size: the window's own pixels, or at most 1280 wide so a clip attaches to
/// a pull request as it is. Even, because H.264 refuses an odd dimension.
public func videoSize(width: Double, height: Double, full: Bool) -> (width: Int, height: Int) {
  var w = width
  var h = height
  if !full && w > 1280 {
    h = h * 1280 / w
    w = 1280
  }
  return (Int(w) / 2 * 2, Int(h) / 2 * 2)
}
