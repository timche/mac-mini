import Testing

@testable import WindowCore

@Test(arguments: [
  [], ["list"], ["list", "Finder"], ["list", "0"], ["list", "-3"], ["bogus", "42"], ["list", "42", "extra"],
  ["shot", "42"], ["shot", "42", "a.png", "--window"], ["shot", "42", "a.png", "--window", "front"],
  ["shot", "42", "a.png", "--max", "3"], ["shot", "42", "a.png", "--", "true"], ["list", "42", "--window", "1"],
  ["record", "42", "a.mp4", "--max", "0"], ["record", "42", "a.mp4", "--max", "soon"], ["record", "42", "a.mp4", "--"],
  ["record", "42", "a.mp4", "--bogus"], ["record", "42"], ["record", "42", "a.mp4", "b.mp4"],
])
func refusesWhatIsNotAPidAndItsOptions(_ arguments: [String]) {
  #expect(parse(arguments) == .failure(.usage))
}

@Test func refusesARecordingInAnyOtherContainer() {
  #expect(parse(["record", "42", "a.gif"]) == .failure(.container("a.gif")))
}

@Test func helpIsAskedForEitherWay() {
  #expect(parse(["-h"]) == .success(.help))
  #expect(parse(["--help"]) == .success(.help))
}

@Test func listTakesAPid() {
  #expect(parse(["list", "42"]) == .success(.list(pid: 42)))
}

@Test func shotTakesAWindowIdAnywhereAfterThePid() {
  #expect(parse(["shot", "42", "a.png"]) == .success(.shot(pid: 42, out: "a.png", window: nil)))
  #expect(parse(["shot", "42", "--window", "7", "a.png"]) == .success(.shot(pid: 42, out: "a.png", window: 7)))
}

@Test func recordDefaults() {
  #expect(parse(["record", "42", "a.mov"]) == .success(.record(pid: 42, window: nil, Recording(out: "a.mov", container: .mov))))
}

@Test func recordTakesItsOptionsAndLeavesTheCommandsOwnAlone() {
  let parsed = parse([
    "record", "42", "a.mp4", "--window", "7", "--max", "2.5", "--no-cursor", "--full", "--", "./drive.sh", "--window",
    "--",
  ])
  let want = Recording(
    out: "a.mp4", container: .mp4, maxSeconds: 2.5, showCursor: false, full: true,
    command: ["./drive.sh", "--window", "--"])
  #expect(parsed == .success(.record(pid: 42, window: 7, want)))
}

@Test func aWrappedCommandEndsAsAShellReportsIt() {
  #expect(exitStatus(signalled: false, status: 7) == 7)
  #expect(exitStatus(signalled: true, status: 15) == 143)
}

@Test func aWideWindowIsScaledTo1280AndEveryDimensionIsEven() {
  #expect(videoSize(width: 1601, height: 500, full: false) == (1280, 398))
  #expect(videoSize(width: 1601, height: 533, full: true) == (1600, 532))
  #expect(videoSize(width: 501, height: 283, full: false) == (500, 282))
}
