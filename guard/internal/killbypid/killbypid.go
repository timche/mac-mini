// Package killbypid refuses a kill that picks its target by name, pattern or port.
//
// Several Claude sessions share this Mac, one worktree each, and each runs its own copy of
// the same dev server, so a process named by its name, a pattern or the port it listens on
// is every session's copy of it: a `pkill -f portless` meant for one session's server
// stopped another's. A kill is allowed by pid alone, and a pid read off pgrep, lsof, ps or
// fuser is a pid chosen by name or port all the same.
package killbypid

import (
	"errors"

	"github.com/timche/mac-mini/guard/internal/shell"
)

const Reason = "Killing by name, pattern or port reaches every session's copy of that process on this " +
	"Mac, not only yours: several Claude sessions share it, one worktree each. Stop only what " +
	"you started: `kill <pid>` with the PID you recorded when you started it (`$!`), or " +
	"TaskStop on the background task that runs it."

var denied = errors.New("killing by name, pattern or port")

var killersByName = map[string]bool{"pkill": true, "killall": true, "killall5": true, "skill": true}

// What picks a process by name or by port: whatever one of these prints is a pid chosen
// the way a pkill would have chosen it.
var finders = map[string]bool{
	"pgrep": true, "pidof": true, "lsof": true, "fuser": true,
	"ps": true, "netstat": true, "ss": true,
}

// Denies reports whether a command kills by name, pattern or port. A command it cannot
// read goes through: a guard that refused what it could not parse would cost every
// session its shell.
func Denies(command string) bool {
	return errors.Is(check(command, false, nil, 0), denied)
}

func check(command string, piped bool, tainted map[string]bool, depth int) error {
	if depth > shell.MaxDepth {
		return shell.ErrParse
	}
	if tainted == nil {
		tainted = map[string]bool{}
	}
	tokens, err := shell.Lex(command)
	if err != nil {
		return err
	}
	parts := shell.Segments(tokens)

	// `done < <(pgrep x)` feeds a loop whose kill comes before the feed is read, so a
	// finder feeding a closing keyword counts from the start of the command.
	for _, part := range parts {
		if len(part.Words) > 0 && !shell.IsCloser(part.Words[0].Text) {
			continue
		}
		fed, err := fedByFinder(part.Feeds, depth)
		if err != nil {
			return err
		}
		if fed {
			piped = true
			break
		}
	}

	nest, pipedAt := 0, -1
	if piped {
		pipedAt = 0
	}

	for _, part := range parts {
		if len(part.Words) > 0 {
			switch first := part.Words[0].Text; {
			case shell.IsOpener(first):
				nest++
			case shell.IsCloser(first):
				nest--
			}
		}

		fed, err := fedByFinder(part.Feeds, depth)
		if err != nil {
			return err
		}
		fed = fed || pipedAt >= 0

		for _, feed := range part.Feeds {
			for _, sub := range feed.Subs {
				if err := check(sub, pipedAt >= 0, nil, depth+1); err != nil {
					return err
				}
			}
		}

		for _, word := range part.Words {
			for _, sub := range word.Subs {
				if err := check(sub, pipedAt >= 0, nil, depth+1); err != nil {
					return err
				}
			}
			if name, ok := shell.AssignName(word.Text); ok {
				found, err := usesFinderIn(word.Subs, depth)
				if err != nil {
					return err
				}
				if found {
					tainted[name] = true
				}
			}
		}

		if len(part.Words) > 3 && part.Words[2].Text == "in" &&
			(part.Words[0].Text == "for" || part.Words[0].Text == "select") {
			var subs []string
			for _, word := range part.Words[3:] {
				subs = append(subs, word.Subs...)
			}
			found, err := usesFinderIn(subs, depth)
			if err != nil {
				return err
			}
			if found {
				tainted[part.Words[1].Text] = true
			}
		}

		if at := shell.CommandOf(part.Words); at >= 0 {
			name, args := shell.Base(part.Words[at].Text), part.Words[at+1:]

			if killersByName[name] {
				return denied
			}
			if name == "fuser" {
				for _, arg := range args {
					if shell.HasFlagLetter(arg.Text, 'k') {
						return denied
					}
				}
			}
			if name == "kill" {
				if err := checkKill(args, tainted, fed, depth); err != nil {
					return err
				}
			}
			// A pid the loop is about to read is a pid a finder chose.
			if name == "read" && fed {
				for _, arg := range args {
					if len(arg.Text) == 0 || arg.Text[0] != '-' {
						tainted[arg.Text] = true
					}
				}
			}
			if shell.Shells[name] {
				for j := 0; j+1 < len(args); j++ {
					if shell.IsDashC(args[j].Text) {
						if err := check(args[j+1].Text, fed, tainted, depth+1); err != nil {
							return err
						}
						break
					}
				}
			}
			if finders[name] && shell.IsPipe(part.End) && pipedAt < 0 {
				pipedAt = nest
			}
		}

		switch {
		case part.End == "(":
			nest++
		case part.End == ")":
			nest--
		case !shell.IsPipe(part.End) && pipedAt >= 0 && nest <= pipedAt:
			pipedAt = -1
		}
	}
	return nil
}

func checkKill(args []*shell.Word, tainted map[string]bool, piped bool, depth int) error {
	if piped {
		return denied
	}

	var targets []*shell.Word
	for i := 0; i < len(args); i++ {
		arg := args[i].Text
		switch {
		// `kill -l` lists the signals and kills nothing.
		case arg == "-l" || arg == "-L":
			return nil
		case arg == "--":
			targets = append(targets, args[i+1:]...)
			i = len(args)
		case arg == "-s" || arg == "-n":
			i++
		case len(arg) > 1 && arg[0] == '-' && len(targets) == 0 && i == 0:
		default:
			targets = append(targets, args[i])
		}
	}

	for _, target := range targets {
		// Every process the account can signal.
		if target.Text == "-1" {
			return denied
		}
		found, err := usesFinderIn(target.Subs, depth)
		if err != nil {
			return err
		}
		if found {
			return denied
		}
		if name, ok := shell.PlainVar(target.Raw); ok && tainted[name] {
			return denied
		}
	}
	return nil
}

func usesFinder(command string, depth int) (bool, error) {
	if depth > shell.MaxDepth {
		return false, shell.ErrParse
	}
	tokens, err := shell.Lex(command)
	if err != nil {
		return false, err
	}
	for _, part := range shell.Segments(tokens) {
		if at := shell.CommandOf(part.Words); at >= 0 && finders[shell.Base(part.Words[at].Text)] {
			return true, nil
		}
		for _, word := range part.Words {
			found, err := usesFinderIn(word.Subs, depth)
			if err != nil || found {
				return found, err
			}
		}
		for _, feed := range part.Feeds {
			found, err := usesFinderIn(feed.Subs, depth)
			if err != nil || found {
				return found, err
			}
		}
	}
	return false, nil
}

func usesFinderIn(subs []string, depth int) (bool, error) {
	for _, sub := range subs {
		found, err := usesFinder(sub, depth+1)
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}

func fedByFinder(feeds []*shell.Feed, depth int) (bool, error) {
	for _, feed := range feeds {
		found, err := usesFinderIn(feed.Subs, depth)
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}
