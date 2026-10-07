// Package watch is hachiko's five-minute check on a Mac with no screen, looking for the
// two failures nobody is there to notice: a process logging into a file nothing bounds,
// and a process burning a core for an hour after whatever wanted it has gone. It runs from
// the io.github.timche.hachiko LaunchAgent.
//
// Nothing here kills anything, and the one thing it changes is a truncate. An alert is a
// one-line message the moment something fires, an on-call session in herdr that
// investigates and reports the analysis itself, and hachiko's own raw details if that
// session never reports — the session is the better alert and the worse guarantee, so
// hachiko never depends on it for the first word.
package watch

import (
	"fmt"
	"os"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/discord"
	"github.com/timche/mac-mini/hachiko/internal/statedir"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

// A dry run changes nothing at all, the state directory and the lock included.
func Run(cfg config.Config, dry bool) error {
	return sweeper{
		cfg:   cfg,
		deps:  realDeps(cfg),
		store: statedir.Store{Dir: cfg.StateDir},
		dry:   dry,
	}.run()
}

func TestAlert(cfg config.Config) error {
	free, err := freeKB(cfg.Home)
	if err != nil {
		return err
	}

	message := wording.Lead(wording.MarkerInfo, "Test alert from hachiko — nothing is wrong").
		Field(wording.LabelFreeSpace, wording.GBUnit(free)).
		About("", cfg.Host).
		String()

	if _, err := discord.SendThroughOP(cfg, discord.Outgoing{Text: message}); err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "%s hachiko: sent a test alert\n", time.Now().Format("2006-01-02T15:04:05-0700"))
	return nil
}
