package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/shibuya"
)

func TestEverySweepTellsShibuyaWhatItRead(t *testing.T) {
	f := newFixture(t)
	f.freeGB = 787
	f.grow("tmp/worker.log", 3*mb)

	equal(t, f.sweep(), "", "a quiet check's log")

	equal(t, len(f.checkins), 1, "check-ins")
	equal(t, f.checkins[0].Failed, false, "whether the sweep reported a fault")
	equal(t, f.checkins[0].FreeGB, 787.0, "the free space reported")
	equal(t, f.checkins[0].OpenIncidents, 0, "the open incidents reported")
	equal(t, f.checkins[0].HotProcesses, 0, "the hot processes reported")
}

// An incident being worked is part of what the Mac is doing, so a recovery message says
// what it came back to rather than only that it came back.
func TestAnOpenIncidentIsCounted(t *testing.T) {
	f := newFixture(t)
	f.grow("tmp/worker.log", 3*mb)
	f.at(0).sweep()

	f.grow("tmp/worker.log", 4*mb)
	f.at(300).sweep()

	equal(t, f.checkins[len(f.checkins)-1].OpenIncidents, 1, "the open incidents reported")
}

// The three faults of the watch rather than of the Mac. Each of them is a sweep that ran
// and came back knowing less than it should, which is exactly what nothing on this machine
// can report about itself.
func TestAWalkThatRanOutOfSecondsIsAFailedSweep(t *testing.T) {
	f := newFixture(t)
	f.cutShort = true

	f.sweep()

	equal(t, f.checkins[0].Failed, true, "whether the sweep reported a fault")
	wants(t, f.checkins[0].Reason, "saw only part of the disk")
}

func TestNoProcessSampleIsAFailedSweep(t *testing.T) {
	f := newFixture(t)
	f.procsErr = errors.New("ps: no such process")

	f.sweep()

	equal(t, f.checkins[0].Failed, true, "whether the sweep reported a fault")
	wants(t, f.checkins[0].Reason, "no process sample")
}

func TestAnUnreadableStateIsAFailedSweep(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(f.cfg.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.cfg.StateDir, "state.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	f.sweep()

	equal(t, f.checkins[0].Failed, true, "whether the sweep reported a fault")
	wants(t, f.checkins[0].Reason, "the state file could not be read")
}

// Two faults in one sweep are one check-in, because they are one sweep.
func TestTwoFaultsAreOneReason(t *testing.T) {
	f := newFixture(t)
	f.cutShort = true
	f.procsErr = errors.New("ps: no such process")

	f.sweep()

	equal(t, len(f.checkins), 1, "check-ins")
	wants(t, f.checkins[0].Reason, "saw only part of the disk")
	wants(t, f.checkins[0].Reason, "no process sample")
}

// A dry run changes nothing anywhere, and a check-in is a change at the other end: a Mac
// told it is being watched on a run that watched nothing.
func TestADryRunChecksInWithNothing(t *testing.T) {
	f := newFixture(t)
	f.grow("tmp/worker.log", 3*mb)

	out := f.dryRun()

	equal(t, len(f.checkins), 0, "check-ins")
	wants(t, out, "would check in with shibuya")
}

func TestADryRunSaysWhatItWouldReportAsAFault(t *testing.T) {
	f := newFixture(t)
	f.cutShort = true

	out := f.dryRun()

	equal(t, len(f.checkins), 0, "check-ins")
	wants(t, out, "would tell shibuya this sweep did not finish its job")
}

// This runs every five minutes for ever. Twelve lines an hour about a token that is still
// missing would bury the lines that matter, so the state is what decides whether anything
// is said at all.
func TestTheSwitchIsSaidOncePerStateChange(t *testing.T) {
	f := newFixture(t)
	f.checkinState = shibuya.NoToken
	f.checkinSay = "there is no ping token, so nothing is watching hachiko"

	wants(t, f.sweep(), "there is no ping token")
	equal(t, f.sweep(), "", "the second check's log")

	f.checkinState, f.checkinSay = shibuya.Sent, ""
	wants(t, f.sweep(), "shibuya is hearing from this Mac again")
	equal(t, f.sweep(), "", "the check after that one")
}

// The first sweep on a fresh Mac is a state change too, and the one thing it should not do
// is announce that a switch nothing has ever used is working.
func TestAFirstCheckInSaysNothing(t *testing.T) {
	f := newFixture(t)

	equal(t, f.sweep(), "", "the first check's log")
	equal(t, f.state().Switch, shibuya.Sent, "the remembered switch state")
}

// The name is the one the Mac is configured with and not the hostname: "mac-mini" is the
// router's spelling, and shibuya's post title is read by a person.
func TestTheSweepSendsTheConfiguredDisplayName(t *testing.T) {
	f := newFixture(t)
	f.cfg.Display = "Mac mini"
	f.at(0).sweep()

	equal(t, len(f.checkins), 1, "check-ins")
	equal(t, f.checkins[0].Display, "Mac mini", "the display name the sweep reported")
}
