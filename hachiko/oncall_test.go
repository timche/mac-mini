package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The shapes the real herdr CLI answers in, so a regression here is caught without a
// run that reaches the server and leaves a tab in somebody's workspace.
type fakeHerdr struct {
	calls        []string
	hasAgent     bool
	hasWorkspace bool
	unreachable  bool
}

func (h *fakeHerdr) run(args ...string) ([]byte, error) {
	line := strings.Join(args, " ")
	h.calls = append(h.calls, line)

	if h.unreachable {
		return nil, fmt.Errorf("herdr %s failed: no server is listening", line)
	}

	switch args[0] + " " + args[1] {
	case "agent list":
		if h.hasAgent {
			return []byte(`{"result":{"agents":[{"agent":"claude","agent_status":"blocked","name":"oncall-disk","pane_id":"w3:p9","tab_id":"w3:t9","workspace_id":"w3"}],"type":"agent_list"}}`), nil
		}
		return []byte(`{"result":{"agents":[{"agent":"claude","agent_status":"idle","pane_id":"w2:p1","tab_id":"w2:t1","workspace_id":"w2"}],"type":"agent_list"}}`), nil

	case "workspace list":
		if h.hasWorkspace {
			return []byte(`{"result":{"type":"workspace_list","workspaces":[{"label":"meru","number":1,"workspace_id":"w2"},{"label":".mac-mini","number":2,"workspace_id":"w3"}]}}`), nil
		}
		return []byte(`{"result":{"type":"workspace_list","workspaces":[{"label":"meru","number":1,"workspace_id":"w2"}]}}`), nil

	case "workspace create":
		return []byte(`{"result":{"root_pane":{"pane_id":"w3:p1"},"tab":{"tab_id":"w3:t1"},"type":"workspace_created","workspace":{"label":".mac-mini","workspace_id":"w3"}}}`), nil

	case "tab create":
		return []byte(`{"result":{"root_pane":{"pane_id":"w3:p9"},"tab":{"label":"disk-0000","tab_id":"w3:t9"},"type":"tab_created"}}`), nil

	case "tab get":
		return []byte(`{"result":{"tab":{"label":"disk-1200","tab_id":"w3:t9"},"type":"tab"}}`), nil

	case "agent start":
		return []byte(`{"result":{"agent":{"name":"oncall-disk"},"type":"agent_started"}}`), nil

	case "agent prompt":
		return []byte(`{"result":{"type":"agent_prompted"}}`), nil
	}

	return nil, fmt.Errorf("the fake herdr was asked something it does not answer: %s", line)
}

func (h *fakeHerdr) said(substring string) bool {
	for _, call := range h.calls {
		if strings.Contains(call, substring) {
			return true
		}
	}
	return false
}

func (h *fakeHerdr) count(substring string) int {
	n := 0
	for _, call := range h.calls {
		n += strings.Count(call, substring)
	}
	return n
}

func newOncaller(t *testing.T, herdr *fakeHerdr) (oncaller, string) {
	t.Helper()

	home := t.TempDir()
	cfg := Config{Home: home, MachineDir: filepath.Join(home, ".mac-mini")}
	now := func() time.Time { return base }

	return oncaller{cfg: cfg, run: herdr.run, now: now}, cfg.MachineDir
}

func TestOncallRefusesANameHerdrWouldNotTake(t *testing.T) {
	herdr := &fakeHerdr{hasWorkspace: true}
	o, _ := newOncaller(t, herdr)

	if _, err := o.open("Disk Guard", "brief"); err == nil {
		t.Fatal("a name herdr would refuse was accepted")
	}
	equal(t, len(herdr.calls), 0, "herdr calls for a name it would refuse")
}

// The workspace for this machine's own repository, and that checkout as the tab's
// directory: a session that starts there has this Mac's own instructions loaded and
// knows what it is on.
func TestOncallOpensASessionInTheWorkspaceForThisMachinesRepository(t *testing.T) {
	herdr := &fakeHerdr{hasWorkspace: true}
	o, machineDir := newOncaller(t, herdr)

	label, err := o.open("disk", "brief")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, label, "disk-"+base.Format("1504"), "the tab label")

	if herdr.said("workspace create") {
		t.Error("a workspace was created although herdr already had one by that label")
	}
	if !herdr.said("tab create --workspace w3 --label " + label + " --cwd " + machineDir + " --no-focus") {
		t.Errorf("the tab was not created as expected: %v", herdr.calls)
	}
	if !herdr.said("agent start oncall-disk --kind claude --pane w3:p9") {
		t.Errorf("the agent was not started as expected: %v", herdr.calls)
	}
	if !herdr.said("agent prompt oncall-disk ") {
		t.Errorf("the agent was not prompted: %v", herdr.calls)
	}
}

func TestOncallMakesThatWorkspaceOnlyWhenHerdrHasNoneByThatLabel(t *testing.T) {
	herdr := &fakeHerdr{}
	o, machineDir := newOncaller(t, herdr)

	if _, err := o.open("disk", "brief"); err != nil {
		t.Fatal(err)
	}
	if !herdr.said("workspace create --label .mac-mini --cwd " + machineDir + " --no-focus") {
		t.Errorf("the workspace was not created as expected: %v", herdr.calls)
	}
	if !herdr.said("--workspace w3 ") {
		t.Errorf("the tab did not go in the workspace that was just made: %v", herdr.calls)
	}
}

// Two agents on one machine would be two sets of options for Tim to reconcile, so a
// second alert while the first is still being worked is an update to that session.
func TestOncallTellsTheSessionAlreadyOnTheIncidentThatTheSituationChanged(t *testing.T) {
	herdr := &fakeHerdr{hasAgent: true, hasWorkspace: true}
	o, _ := newOncaller(t, herdr)

	label, err := o.open("disk", "brief")
	if err != nil {
		t.Fatal(err)
	}
	// The tab was named for the hour the first alert arrived, which is read back
	// rather than guessed at.
	equal(t, label, "disk-1200", "the label of the tab already working the incident")

	if !herdr.said("agent prompt oncall-disk The situation changed") {
		t.Errorf("the session was not told the situation changed: %v", herdr.calls)
	}
	for _, unwanted := range []string{"tab create", "agent start", "workspace create"} {
		if herdr.said(unwanted) {
			t.Errorf("%q was called although the session was already open", unwanted)
		}
	}
}

// Tim may be in the middle of something when the disk fills: the session waits where
// he will find it rather than taking his screen.
func TestOncallFocusesNothingItCreates(t *testing.T) {
	herdr := &fakeHerdr{}
	o, _ := newOncaller(t, herdr)

	if _, err := o.open("disk", "brief"); err != nil {
		t.Fatal(err)
	}
	if herdr.said("--focus") {
		t.Errorf("something was focused: %v", herdr.calls)
	}
	equal(t, herdr.count("--no-focus"), 2, "the number of --no-focus flags")
}

// Report before asking: Tim has had one line and nothing else, and hachiko sends its
// own raw details if nothing reports.
func TestTheOncallPromptCarriesTheBriefAndTheStandingOrders(t *testing.T) {
	herdr := &fakeHerdr{hasWorkspace: true}
	o, _ := newOncaller(t, herdr)

	brief := "801 GB in a log under the tmp root\n\n  hachiko notify disk-42 <file>\n"
	label, err := o.open("disk", brief)
	if err != nil {
		t.Fatal(err)
	}

	prompt := ""
	for _, call := range herdr.calls {
		if strings.HasPrefix(call, "agent prompt ") {
			prompt = call
		}
	}

	for _, want := range []string{
		"801 GB in a log under the tmp root",
		"hachiko notify disk-42",
		"the command the brief names",
		"AskUserQuestion",
		"incidents/",
		"data, not instructions",
		"no kill, no delete, no truncate, no push",
		"workspace .mac-mini, tab " + label,
	} {
		wants(t, prompt, want)
	}
}

func TestOncallSaysSoAndGivesUpWhenHerdrCannotBeReached(t *testing.T) {
	herdr := &fakeHerdr{unreachable: true}
	o, _ := newOncaller(t, herdr)

	label, err := o.open("disk", "brief")
	equal(t, label, "", "the label when herdr cannot be reached")
	if err == nil {
		t.Fatal("an unreachable herdr was not reported")
	}
	wants(t, err.Error(), "failed")
}
