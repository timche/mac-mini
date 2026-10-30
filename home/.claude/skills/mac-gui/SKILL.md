---
name: mac-gui
description: "How to launch, drive, click, type into or screenshot an app window on this Mac without stealing the foreground from other sessions: Chrome and Chrome for Testing with a window, Electron apps, native macOS menus and dialogs, UTM VMs, notifications. Load before starting any app or browser that shows a window, and before any keystroke, click or screenshot of one."
---

# Driving a window on this Mac

Every session runs in one GUI session with one frontmost app, one key window and one pointer. A keystroke or click that needs the foreground lands in whatever is in front, which may be another session's app. So reach your app without the foreground, and when nothing can, take the foreground only under `focus-lock`.

## Pick the route

| App | Drive it with | Raises it, so never outside `focus-lock` |
|---|---|---|
| Chrome, Chrome for Testing | CDP. Launch with `--no-startup-window` and a remote debugging port, then open each window with `Target.createTarget {newWindow: true, background: true}` | A launch without `--no-startup-window` (`open -g` too), `Page.bringToFront`, `Target.activateTarget`, `createTarget` without `background: true` |
| chrome-devtools MCP | `new_page` with `background: true`, `select_page` without `bringToFront` | `select_page` with `bringToFront` |
| Playwright | `goto`, `click`, `keyboard.type`, `screenshot`. A new page is `Target.createTarget` with `background: true` over `browser.newBrowserCDPSession()`, then `context.waitForEvent("page")` | `context.newPage()` |
| Electron | CDP. The app shows its first window with `showInactive()`; an unattended run calls `app.setActivationPolicy("prohibited")` before its first window | A plain launch (first `show()`), `focus()`, `webContents.focus()` |
| Native menu item | `perform action "AXPress"` on the item of `(first process whose unix id is <pid>)` in System Events | Whatever the item's own handler raises |
| Windows guest in UTM (QEMU) | UTM's AppleScript: `input keystroke`, `input scan code`, `input mouse click … at {x, y}` | UTM raises the VM's window when it starts |
| macOS guest (Apple backend) | VNC into the guest's own Screen Sharing: `uvx --with pycryptodomex vncdotool -s <ip>::5900 …` | |

CDP key events reach the page, never the app menu, so a menu shortcut goes through `AXPress` instead. The public web and anything signed in as Tim go through Claude in Chrome, not a browser you start.

## When only the foreground will do

System Events `keystroke` and `key code`, `cliclick`, raising a window, a native dialog or the system's own UI, an app that activates itself on launch. Write the whole sequence, from raising the window to the shot that checks it, as one script and run it as `focus-lock <script>`. Keep it short, since every other session waits behind it. Inside it, address the app by pid and check it is frontmost before each keystroke or click.

`guard` refuses `cliclick` and an `osascript` that types, raises or activates outside `focus-lock`, but it reads only the command line: inside a script file, or a program that raises a window itself, the rule is yours to keep.

## Seeing it

- `window-shot <pid> <out.png> [--window <id>]` captures one window of that process even when others cover it. `window-record <pid> <out.mp4> [--window <id>]` records it the same way, without the foreground, until you send SIGINT; start it in the background and wait for its `recording` line on stderr before driving the app. Never capture or record the display, which shows every other session's windows.
- Address a window only by values nothing else shares: the pid of the app's main process, kept from its launch, and the window id when the process has several windows, which the refusal lists with their titles. UTM is one process owning every session's VM windows, so a VM is its window id. A process name or a title can belong to another session's app or VM, and the shot of it looks right.
- Cover a window rather than minimise it; a minimised window cannot be captured.
- Look at every shot before saying what it shows.
- A capture that fails or comes back black, or a privacy prompt, means a grant is missing: a blocker for Tim on the Mac's own screen, not something to work around.

## Cleaning up

Close every window and tab you opened. Quit your app by its pid, `kill -TERM <pid>` on the main process, which lets Chromium flush its cookies. `pkill -f` matches the shell running it, so bracket a character: `pkill -f "[u]ser-data-dir=…"`. Never stop another session's app.

## Procedures

`$PROJECT_DOCS_DIR/shared/macos-gui-automation.md` has the measurements behind this and the longer procedures. Read the section when the task needs it:

- "Keyboard through System Events, and always by pid": what osascript may do here, and the Automation prompt that blocks on the Mac's screen
- "Typing a secret": a password into a form without it reaching an argument, a file or your output
- "Signing in to a Google account": no debugging port during sign-in, one app per signed-in profile
- "Capturing one app's window": picking the window by pid and window id for `window-shot`, and recording it with `window-record`
- "Testing notifications": signed builds, banners, Notification Center, action buttons
