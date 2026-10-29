package killbypid

import "testing"

// Every way of naming the process instead of the pid, wherever the kill is written: the
// cases are the specification, and a kill that reaches another session's dev server is a
// case missing from them.
func TestAKillByNameOrPatternIsRefusedWhereverItRuns(t *testing.T) {
	for _, command := range []string{
		`pkill -f "portless/dist/cli.js"`,
		"killall node",
		"killall5",
		"skill -9 node",
		"/usr/bin/pkill -9 vite",
		"cd app && pkill -f vite",
		"sudo -u root killall node",
		"doas killall node",
		"env FOO=1 pkill node",
		"nohup pkill -f vite",
		"timeout 5 pkill -f vite",
		"nice -n 10 pkill node",
		"exec pkill node",
		"echo node | xargs pkill",
		"echo $(pkill -f x)",
		"echo `killall node`",
		`bash -c "pkill -f vite"`,
		`sh -c 'pkill -f vite'`,
		`bash -lc "pkill -f vite"`,
		"PORT=3000 pkill -f vite",
		"if true; then pkill node; fi",
	} {
		if !Denies(command) {
			t.Errorf("%s was allowed; it kills by name", command)
		}
	}
}

func TestAKillWhosePidsComeFromALookupIsRefused(t *testing.T) {
	for _, command := range []string{
		"kill $(pgrep -f vite)",
		`kill "$(pgrep node)"`,
		"kill `pidof node`",
		"kill -9 $(lsof -ti :3000)",
		"pgrep -f vite | xargs kill",
		"lsof -ti:3000 | xargs kill",
		"ps aux | grep x | awk '{print $2}' | xargs kill",
		"pid=$(pgrep -f vite); kill $pid",
		"pid=$(pgrep -f vite); kill ${pid}",
		"for p in $(pgrep node); do kill $p; done",
		"pgrep node | while read p; do kill $p; done",
		"xargs kill < <(pgrep x)",
		"while read p; do kill $p; done < <(pgrep x)",
		`read pid <<< "$(pgrep x)"; kill $pid`,
		`read -r pid <<< $(lsof -ti :3000) && kill -9 "$pid"`,
		"kill $(netstat -anv | grep 3000 | awk '{print $9}')",
		"kill $(ss -ltnp | grep 3000)",
		"fuser -k 3000/tcp",
		"fuser -9k 3000/tcp",
		"kill -- -1",
		"kill -s TERM -1",
		"pgrep -f vite | xargs -n 1 kill",
		"lsof -ti :3000 | xargs kill -9",
		`bash -c "kill $(pgrep node)"`,
	} {
		if !Denies(command) {
			t.Errorf("%s was allowed; its pids come from a lookup", command)
		}
	}
}

func TestAKillByPidAndAPkillThatIsOnlyTextAreAllowed(t *testing.T) {
	for _, command := range []string{
		"kill 4242",
		"kill -TERM 4242",
		"kill %1",
		`kill "$pid"`,
		"kill $PID",
		"kill ${PID}",
		"kill $(cat /tmp/server.pid)",
		"kill -l",
		"pgrep -f vite",
		"lsof -ti :3000",
		"pgrep -f vite | head -1; kill $SERVER_PID",
		"docker compose kill",
		"kill -0 $pid",
		"read pid < server.pid; kill $pid",
		`git commit -m "never pkill"`,
		"grep -n pkill file",
		"command -v pkill",
		"echo 'pkill -f vite'",
		"fuser 3000/tcp",
		"cat <<EOF\npkill -f vite\nEOF",
		"cat <<-EOF\n\tpkill -f vite\n\tEOF",
		"# pkill -f vite\necho hi",
		"echo pkill > notes.txt",
		`gh pr create --body "we no longer pkill anything"`,
	} {
		if Denies(command) {
			t.Errorf("%s was refused; it kills nothing by name", command)
		}
	}
}

// A guard on every session's shell that refused what it could not read would cost them all
// their shell, so a command it cannot parse goes through.
func TestACommandThatCannotBeReadIsAllowed(t *testing.T) {
	for _, command := range []string{
		`echo "unterminated`,
		"echo 'unterminated",
		"echo $(pkill -f x",
		"echo `pkill -f x",
		`pkill -f "x` + "\n",
		"",
	} {
		if Denies(command) {
			t.Errorf("%q was refused; it cannot be read", command)
		}
	}
}

// Nesting is answered with "unreadable" rather than with the stack overflow that would
// exit 2 and block the command.
func TestNestingDeeperThanAnyoneWritesIsAllowed(t *testing.T) {
	command := "pkill x"
	for range 200 {
		command = "echo $(" + command + ")"
	}
	if Denies(command) {
		t.Error("a command nested past what can be read was refused")
	}
}
