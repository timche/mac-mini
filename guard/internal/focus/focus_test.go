package focus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTakingTheForegroundWithoutTheLockIsRefused(t *testing.T) {
	for _, command := range []string{
		"cliclick c:1,1",
		"cliclick -w 10 c:100,200 m:200,300",
		"/opt/homebrew/bin/cliclick c:1,1",
		"sudo cliclick c:1,1",
		"timeout 5 cliclick c:1,1",
		`bash -c "cliclick c:1,1"`,
		"echo $(cliclick p)",
		"echo `cliclick p`",
		"op run --env-file .env.op -- cliclick c:1,1",
		`osascript -e 'tell application "System Events" to keystroke "a"'`,
		`osascript -e 'tell application "System Events"' -e 'key code 36'`,
		`osascript -e 'set frontmost of (first process whose unix id is 42) to true'`,
		`osascript -e 'tell application "System Events" to perform action "AXRaise" of window 1 of process "Finder"'`,
		`osascript -e 'tell application "System Events" to click at {10, 20}'`,
		`osascript -e 'tell application "Finder" to activate'`,
		`osascript -e 'activate application "Finder"'`,
		`osascript -e 'tell application "Finder"' -e 'activate' -e 'end tell'`,
		`osascript -e 'do shell script "cliclick c:1,1"'`,
		`osascript -l AppleScript -e 'keystroke "a"'`,
		`op run --env-file .env.op -- osascript -e 'keystroke secret'`,
		`sudo osascript -e 'KEYSTROKE "a"'`,
		`bash -c "osascript -e 'keystroke \"a\"'"`,
	} {
		if !Denies(command) {
			t.Errorf("%s was allowed; it takes the foreground", command)
		}
	}
}

func TestTheLockAndWhatNeedsNoneAreAllowed(t *testing.T) {
	for _, command := range []string{
		"focus-lock cliclick c:1,1",
		"focus-lock -t 60 cliclick c:1,1",
		"sudo focus-lock cliclick c:1,1",
		`focus-lock osascript -e 'tell application "System Events" to keystroke "a"'`,
		`focus-lock bash -c "cliclick c:1,1"`,
		"op run --env-file .env.op -- focus-lock cliclick c:1,1",
		`op run --env-file .env.op -- focus-lock osascript type.applescript 42 PASSWORD`,
		"cliclick -h",
		"cliclick --help",
		"cliclick -V",
		`osascript -e 'tell application "System Events" to get frontmost of (first process whose unix id is 42)'`,
		`osascript -e 'tell application "System Events" to get name of every process whose frontmost is true'`,
		`osascript -e 'perform action "AXPress" of menu item 1 of menu 1'`,
		`osascript -e 'get position of window 1 of process "Finder"'`,
		`osascript -e 'tell application "UTM" to input keystroke "a" of vm'`,
		`osascript -e 'tell application "UTM" to input scan code {28, 156} of vm'`,
		`osascript -e 'tell application "UTM" to input mouse click at {10, 20} of vm'`,
		`osascript -e 'tell application "UTM" to start virtual machine "win11"'`,
		`osascript -e 'tell application "UTM" to query ip of vm'`,
		`osascript -e 'do shell script "echo hi"'`,
		`git commit -m "run cliclick under focus-lock"`,
		"grep -rn cliclick docs",
		`echo "osascript -e keystroke"`,
		"cat <<EOF\ncliclick c:1,1\nEOF",
		"op read op://dev/vm-ssh/private-key",
		"osascript /no/such/script.applescript",
		"lsof -ti :3000",
	} {
		if Denies(command) {
			t.Errorf("%s was refused; it needs no foreground", command)
		}
	}
}

// The script osascript is handed as a file is what it runs, so it is what gets read.
func TestAScriptFileIsReadLikeItsStatements(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	typing := write("type.applescript", `on run argv
  tell application "System Events"
    set frontmost of (first process whose unix id is 42) to true
    keystroke "secret"
  end tell
end run
`)
	pressing := write("press.applescript", `tell application "System Events"
  perform action "AXPress" of menu item 1 of menu 1 of process "Finder"
end tell
`)
	binary := write("binary.scpt", "FasdUAS\x00\x01keystroke")

	if !Denies("osascript " + typing + " 42 PASSWORD") {
		t.Error("a script file that types into the frontmost app was allowed")
	}
	if Denies("focus-lock osascript " + typing + " 42 PASSWORD") {
		t.Error("a script file that types was refused under the lock")
	}
	if Denies("osascript " + pressing) {
		t.Error("a script file that presses a control through accessibility was refused")
	}
	// A compiled script is not text to read, so nothing can be read off it.
	if Denies("osascript " + binary) {
		t.Error("a compiled script was refused on what could not be read")
	}
}

func TestACommandThatCannotBeReadIsAllowed(t *testing.T) {
	for _, command := range []string{
		`osascript -e 'keystroke "a"`,
		`cliclick "c:1,1`,
		"cliclick $(c:1,1",
		"",
	} {
		if Denies(command) {
			t.Errorf("%q was refused; it cannot be read", command)
		}
	}
}

func TestNestingDeeperThanAnyoneWritesIsAllowed(t *testing.T) {
	command := "cliclick c:1,1"
	for range 200 {
		command = "echo $(" + command + ")"
	}
	if Denies(command) {
		t.Error("a command nested past what can be read was refused")
	}
}
