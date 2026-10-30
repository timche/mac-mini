import Testing

@testable import WindowCore

let utm = [
  WindowInfo(id: 8282, pid: 100, owner: "UTM", title: "Windows 11", width: 1024, height: 808),
  WindowInfo(id: 8280, pid: 100, owner: "UTM", title: "UTM", width: 900, height: 652),
]
let electron = WindowInfo(id: 9001, pid: 200, owner: "Electron", title: "Meru", width: 1280, height: 800)
let otherSession = WindowInfo(id: 9002, pid: 300, owner: "Electron", title: "Meru", width: 1280, height: 800)

@Test func aPidWithOneWindowNeedsNoId() {
  #expect(select(pid: 200, window: nil, in: utm + [electron, otherSession]) == .success(electron))
}

@Test func aPidWithSeveralWindowsIsRefusedWithTheirIdsAndTitles() {
  #expect(
    select(pid: 100, window: nil, in: utm)
      == .failure(
        SelectionError(
          message: "pid 100 has 2 on-screen windows; pick one with --window <id>: 8282 \"Windows 11\", 8280 \"UTM\"")))
}

@Test func anIdPicksOneOfThePidsWindows() {
  #expect(select(pid: 100, window: 8280, in: utm) == .success(utm[1]))
}

@Test func anIdThePidDoesNotOwnIsRefusedEvenWhenAnotherProcessHasIt() {
  #expect(
    select(pid: 200, window: 9002, in: [electron, otherSession])
      == .failure(SelectionError(message: "pid 200 has no on-screen window 9002; its windows: 9001 \"Meru\"")))
}

@Test func aPidWithNoWindowNamesWhatIsOnScreen() {
  #expect(
    select(pid: 7, window: nil, in: utm + [electron])
      == .failure(
        SelectionError(message: "no on-screen window owned by pid 7; on screen: Electron (pid 200), UTM (pid 100)")))
  #expect(
    owned(by: 7, in: [])
      == .failure(SelectionError(message: "no on-screen window owned by pid 7; no app has a window on screen")))
}

@Test func listingIsOneWindowALine() {
  #expect(listing(utm) == "8282 1024x808 \"Windows 11\"\n8280 900x652 \"UTM\"\n")
}
