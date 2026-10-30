# window

`window` lists, captures and records one app window on this Mac, where `screencapture` alone takes the whole display with every other session's windows on it, or a rectangle of it that holds the window only while it is raised and uncovered, which needs the foreground. Every session shares one GUI desktop, so a session shows its own app this way and nothing else.

```text
window list <pid>
window shot <pid> <out.png> [--window <id>]
window record <pid> <out.mp4|out.mov> [--window <id>] [--max <seconds>] [--no-cursor] [--full] [-- <command> ...]
```

## Naming the window

A window is named only by values nothing else shares: the pid of the app's main process, and the window id when that process has more than one window. Every session's development build of one app owns its windows under the same process name, often with the same title, and UTM's one process owns every session's VM windows under titles that are only the VM's name, so a name or a title can pick another session's window and the capture of it looks right. So the first argument is a pid and nothing else, a pid with several on-screen windows is refused with each one's id and title, and `--window <id>` picks one; `list` prints the same ids with each window's size. A pid with no on-screen window exits 1 and names what is on screen instead, with pids. The window list is CoreGraphics' on-screen list, which a minimised window and one on a Space that is not showing leave.

## shot

`screencapture -x -o -l <id>`, without its shadow, Electron's child views included: the window id is the one thing `screencapture` cannot find for itself. A still capture has the window's ordinary buttons.

## record

ScreenCaptureKit's desktop-independent window filter, which records the window's own pixels while others cover it and raises nothing, so it needs neither the foreground nor `focus-lock`. The video is H.264 through `SCRecordingOutput`, scaled by ScreenCaptureKit to at most 1280 pixels wide so it attaches to a pull request as it is, with the pointer and its clicks drawn unless `--no-cursor`; `--full` keeps the window's own pixels.

Given `-- <command>`, it starts the command once the first frame is in, finishes the file when the command exits and exits with the command's status, 128 plus the signal for one that was killed, so a recording ends with the script that drives it rather than running on to `--max` when its session is gone. The command gets `WINDOW_RECORD_START_MS`, the recording's start in epoch milliseconds, which the `recording … from <ms> ms` line on stderr also carries. Without a command it runs until SIGINT or SIGTERM, which it passes on to a command, or `--max` seconds (300 by default), and prints the file once it is finished.

What macOS does around a recording, and what the tool cannot change:

- It shows its recording indicator in the menu bar and draws a grey sharing pill over the window's traffic lights, which the video shows.
- A minimised window delivers no frames and fails the recording on its first.
- The video is what the app draws, and some apps stop drawing a window none of which is visible: UTM's VM windows stop entirely under a full cover and draw on with a few pixels showing. The measurements are in `$PROJECT_DOCS_DIR/shared/macos-gui-automation.md`, under "Recording one window", with the `ffmpeg-full` recipe that labels a clip.

Only the Screen Recording grant is needed, and it is herdr-launcher's: macOS holds the responsible process accountable for its children, so a rebuilt binary costs no grant.

## Building and testing

A Swift package on Apple's SDK alone, built on the way past by `home/.local/libexec/build-tool` with Xcode's toolchain like the Go tools are with mise's: the wrapper in `home/.local/bin/window` fingerprints the sources, rebuilds into `~/Library/Caches/window` when they change, and runs `swift test` as the gate before the new binary replaces the last one. `WINDOW_CACHE_DIR` overrides the cache. A call that has nothing to build costs a few hundredths of a second, where `swift -` interpreting the same source cost two.

`WindowCore` holds everything that decides — arguments, which window, the listing, the video size, a wrapped command's status — and its tests are the specification: `swift test --package-path tools/window`. `Sources/window` is the part that touches the window server and ScreenCaptureKit, which `test/assert.sh` checks only at the edges.
