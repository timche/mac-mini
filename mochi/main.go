// mochi is Tim's own Claude Tag for Discord: he mentions the bot in a server it is in, and a
// Claude Code session starts in that server's herdr workspace with his instruction and the
// conversation around it. `mochi listen` is the gateway listener the LaunchAgent runs;
// `mochi post` and `mochi history` are for the sessions it starts.
//
// Nothing in this file does any of it: it parses the command and hands it to the package
// that owns it.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	discordrest "github.com/timche/mac-mini/discord/rest"

	"github.com/timche/mac-mini/mochi/internal/amend"
	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/config"
	"github.com/timche/mac-mini/mochi/internal/event"
	"github.com/timche/mac-mini/mochi/internal/history"
	"github.com/timche/mac-mini/mochi/internal/leaks"
	"github.com/timche/mac-mini/mochi/internal/listen"
	"github.com/timche/mac-mini/mochi/internal/mark"
	"github.com/timche/mac-mini/mochi/internal/op"
	"github.com/timche/mac-mini/mochi/internal/origin"
	"github.com/timche/mac-mini/mochi/internal/post"
	"github.com/timche/mac-mini/mochi/internal/self"
	"github.com/timche/mac-mini/mochi/internal/split"
	"github.com/timche/mac-mini/mochi/internal/state"
)

const usage = `usage: mochi [listen]
       mochi post [--review | --done [--close]] [--pr <url>] [--type <kind>] [--file <path>]... [<text>]
       mochi history [--post] [--before <id> | --after <id> | --around <id>] [--limit <n>]
       mochi announce [--pr <url>] [--file <path>]... [<text>]
       mochi edit [--drop-files] [--pr <url>] <message link> [<text>]
       mochi delete <message link>

  listen    connect to Discord and start a Claude Code session in herdr whenever Tim
            mentions mochi; what the LaunchAgent runs, and what no command means
  post      post into this session's Discord thread, with no pings; --file attaches
            a screenshot, a recording or any other file, and may be given up to
            ten times; --review when it needs an answer, a result or a question
            (❓), --done when the task is complete with nothing left for him (✅),
            and --close with it once the session has cleaned up, for mochi to close
            its tab a minute later when Tim last wrote from Discord;
            --pr adds a pull request as "#<number> (<url>)"; the only other links
            allowed are https ones to the server's allowed domains;
            --type refiles a forum post as feature, bug, improvement or research,
            and needs no text; with no text argument the text is read from stdin
  history   print more of the channel or thread this session started from, oldest
            first and fenced as data, with attachments downloaded; --post reads the
            session's own thread or post instead; one of --before, --after or
            --around a message id, and --limit up to 100 (default 50)
  announce  post to the announce channel of the server whose repository this checkout
            is of, with no pings, held to everything post is held to; prints the
            message's link
  edit      replace the text of a message mochi posted, held to what post is held
            to and pinging nobody; --drop-files removes its attachments too
  delete    take down a message mochi posted
            edit and delete act only on mochi's own messages in a mapped server's
            announce channel, or in a mochi session's own thread or post; a
            thread's or post's first message is edited, never deleted

post and history run only inside a session mochi started; the rest in any session.
`

type usageError struct{ error }

// Said before `op run`, so a command outside a session costs no 1Password read.
var errNoSession = errors.New("this runs only inside a session mochi started, and MOCHI_THREAD is not set")

func main() {
	err := run(os.Args[1:])
	var bad usageError
	switch {
	case err == nil:
	case errors.As(err, &bad):
		fmt.Fprintf(os.Stderr, "mochi: %v\n%s", err, usage)
		os.Exit(2)
	case errors.Is(err, self.ErrReplaced):
		fmt.Fprintf(os.Stderr, "mochi: %v\n", err)
		os.Exit(3)
	default:
		fmt.Fprintf(os.Stderr, "mochi: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// Left out of the usage: Claude Code's Stop and AskUserQuestion hooks run it in every
	// session, so it reads no config, needs no token and prints nothing.
	if len(args) > 0 && args[0] == "session-event" {
		if len(args) == 2 {
			event.Write(os.Stdin, os.Getenv, config.EventsDirFromEnv(), args[1], time.Now)
		}
		return nil
	}

	withToken := len(args) > 0 && args[0] == op.WithToken
	if withToken {
		args = args[1:]
	}
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Print(usage)
		return nil
	}
	// Left out of the usage: the listener runs it through the wrapper so that an edit is
	// built, and it needs nothing else to have happened.
	// --check-config is the listener's preflight of a new build before it execs it.
	if len(args) > 0 && args[0] == "version" {
		if len(args) > 1 && args[1] == "--check-config" {
			if _, err := config.FromEnv(); err != nil {
				return err
			}
		}
		fmt.Println("mochi")
		return nil
	}

	command := "listen"
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}

	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	thread := os.Getenv(listen.ThreadVar)

	switch command {
	case "listen":
		if len(args) > 0 {
			return usageError{errors.New("listen takes no arguments")}
		}
		if withToken {
			return listen.Serve(cfg, log)
		}
		return listen.Run(cfg, log)

	case "post":
		if !withToken {
			if args, err = postStdin(args, os.Stdin); err != nil {
				return err
			}
		}
		p, err := postArgs(args, cfg.Links())
		if err != nil {
			return usageError{err}
		}
		if !withToken {
			if thread == "" {
				return errNoSession
			}
			return op.Reenter(cfg.EnvFile, append([]string{"post"}, args...))
		}
		api, err := rest()
		if err != nil {
			return err
		}
		marks := &mark.Marker{API: api, Path: cfg.MarksPath(), Now: time.Now, Log: log}
		repos := func(guild string) (string, error) {
			server, ok := cfg.Server(guild)
			if !ok {
				return "", errors.New("this session's server is no longer mapped")
			}
			return origin.Of(server.Repo)
		}
		// The session's checkout is the one it runs in, a worktree of its own or the mapped
		// repository, as long as it is a checkout of the server's repository; with it, Claude
		// Code's scratch folder for that checkout and for the mapped repository.
		p.Roots = func(guild string) ([]string, error) {
			want, err := repos(guild)
			if err != nil {
				return nil, err
			}
			wd, err := os.Getwd()
			if err != nil {
				return nil, err
			}
			root, err := origin.Toplevel(wd)
			if err != nil {
				return nil, errors.New("this session is not running in a checkout, so it can attach no file")
			}
			if got, err := origin.Of(root); err != nil || !strings.EqualFold(got, want) {
				return nil, fmt.Errorf("this session is running in a checkout of %s rather than %s, so it can attach no file", got, want)
			}
			server, _ := cfg.Server(guild)
			// The account's own home, not the session's HOME, which it could set to anywhere.
			me, err := user.Current()
			if err != nil {
				return nil, err
			}
			return []string{root, scratch(me.HomeDir, root), scratch(me.HomeDir, server.Repo)}, nil
		}
		p.Leaks = leaks.CLI
		p.Links = func(guild string) []string {
			server, _ := cfg.Server(guild)
			return server.Links
		}
		if err := post.Send(api, state.Store{Path: cfg.StatePath()}, marks, repos, thread, p); err != nil {
			return err
		}
		at := time.Now()
		if p.PR != "" {
			if err := event.Put(cfg.EventsDir(), event.Event{Thread: thread, Kind: event.PR, PR: p.PR, At: at.Unix(), Nanos: at.UnixNano()}); err != nil {
				return err
			}
		}
		if p.Close {
			return event.Put(cfg.EventsDir(), event.Event{Thread: thread, Kind: event.Close, At: at.Unix(), Nanos: at.UnixNano() + 1})
		}
		return nil

	case "announce":
		if !withToken {
			if args, err = textStdin(args, os.Stdin, func(a []string) error { _, err := announceArgs(a, nil); return err }); err != nil {
				return err
			}
		}
		p, err := announceArgs(args, cfg.Links())
		if err != nil {
			return usageError{err}
		}
		server, root, err := announcing(cfg)
		if err != nil {
			return err
		}
		if err := p.Check(server.Links); err != nil {
			return err
		}
		if !withToken {
			if server.Announce == "" {
				return fmt.Errorf("the server for %s has no announce channel", root)
			}
			if p.PR != "" {
				if err := post.CheckRepo(p.PR, server.Guild, repoOf(server)); err != nil {
					return err
				}
			}
			return op.Reenter(cfg.EnvFile, append([]string{"announce"}, args...))
		}
		api, err := rest()
		if err != nil {
			return err
		}
		me, err := user.Current()
		if err != nil {
			return err
		}
		roots := []string{root, scratch(me.HomeDir, root)}
		p.Roots = func(string) ([]string, error) { return roots, nil }
		p.Links = func(string) []string { return server.Links }
		p.Leaks = leaks.CLI
		msg, err := post.Announce(api, server.Guild, server.Announce, repoOf(server), p)
		if err != nil {
			return err
		}
		fmt.Println(chat.Link(server.Guild, server.Announce, msg.ID))
		return nil

	case "edit", "delete":
		var link string
		var change amend.Change
		if command == "edit" {
			if !withToken {
				if args, err = textStdin(args, os.Stdin, func(a []string) error { _, _, err := editArgs(a); return err }); err != nil {
					return err
				}
			}
			link, change, err = editArgs(args)
		} else {
			link, err = deleteArgs(args)
		}
		if err != nil {
			return usageError{err}
		}
		scope := amend.Scope{Servers: cfg.Servers, Store: state.Store{Path: cfg.StatePath()}, Thread: thread}
		_, server, err := scope.Server(link)
		if err != nil {
			return err
		}
		if command == "edit" {
			if err := change.Check(server.Links); err != nil {
				return err
			}
		}
		if !withToken {
			return op.Reenter(cfg.EnvFile, append([]string{command}, args...))
		}
		api, err := rest()
		if err != nil {
			return err
		}
		if command == "delete" {
			return amend.Delete(api, scope, link)
		}
		change.Leaks = leaks.CLI
		change.Repos = repoOf(server)
		if wd, err := os.Getwd(); err == nil {
			change.Base, _ = origin.Toplevel(wd)
		}
		if err := amend.Edit(api, scope, link, change); err != nil {
			return err
		}
		fmt.Println(link)
		return nil

	// Left out of the usage: it is for choosing the split model, and reads cases rather
	// than Discord.
	case "split-eval":
		s, err := splitArgs(cfg.Split, args)
		if err != nil {
			return usageError{err}
		}
		return split.Eval(os.Stdin, os.Stdout, split.Splitter{
			Run: split.CLI, Model: s.Model, Effort: s.Effort, Timeout: split.Timeout,
		}, time.Now)

	case "history":
		q, err := historyArgs(args)
		if err != nil {
			return usageError{err}
		}
		if !withToken {
			if thread == "" {
				return errNoSession
			}
			return op.Reenter(cfg.EnvFile, append([]string{"history"}, args...))
		}
		api, err := rest()
		if err != nil {
			return err
		}
		return history.Show(cfg, api, thread, q, os.Stdout)
	}
	return usageError{fmt.Errorf("unknown command %s", command)}
}

var unslug = regexp.MustCompile(`[^A-Za-z0-9]`)

// Claude Code's scratch folder for a project, where this account's .zshenv points
// CLAUDE_CODE_TMPDIR: under the user's id, the project's path with every character but a letter
// or a digit made a dash. From the account's home rather than the variable, which the session
// could set to anywhere.
func scratch(home, project string) string {
	return filepath.Join(home, ".cache", "claude-tmp", fmt.Sprintf("claude-%d", os.Getuid()), unslug.ReplaceAllString(project, "-"))
}

// REST alone, with no gateway: a post or a page of history is a request or two.
func rest() (chat.API, error) {
	token := os.Getenv(op.TokenVar)
	if token == "" {
		return nil, errors.New("the bot token did not resolve")
	}
	return chat.NewLive(discordrest.New(token)), nil
}

var errNoText = errors.New("the text to post goes as an argument or on stdin")

func postStdin(args []string, stdin io.Reader) ([]string, error) {
	return textStdin(args, stdin, func(a []string) error { _, err := postArgs(a, nil); return err })
}

// With no text among the arguments, the text is stdin, so a post of several lines needs no
// shell quoting. Read here, before `op run`, and handed on as the last argument, since the
// process that runs under op cannot be given back a stdin this one has read.
func textStdin(args []string, stdin io.Reader, parse func(args []string) error) ([]string, error) {
	if err := parse(args); !errors.Is(err, errNoText) {
		return args, nil
	}
	body, err := io.ReadAll(io.LimitReader(stdin, 64<<10))
	if err != nil {
		return nil, err
	}
	return append(append([]string{}, args...), "--", string(body)), nil
}

// Before `op run` the session's server is not known, so its text is held to the domains any
// server allows here, and to its own server's in post.Send.
func postArgs(args []string, links []string) (post.Post, error) {
	var p post.Post
	var review, done bool
	fs := flag.NewFlagSet("post", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Func("file", "", func(path string) error { p.Files = append(p.Files, path); return nil })
	fs.BoolVar(&review, "review", false, "")
	fs.BoolVar(&done, "done", false, "")
	fs.BoolVar(&p.Close, "close", false, "")
	fs.StringVar(&p.PR, "pr", "", "")
	fs.StringVar(&p.Type, "type", "", "")
	if err := fs.Parse(args); err != nil {
		return p, err
	}
	if p.Type != "" && !split.IsType(p.Type) {
		return p, fmt.Errorf("--type is feature, bug, improvement or research, not %s", p.Type)
	}
	switch {
	case review && done:
		return p, errors.New("post takes --review or --done, not both")
	case p.Close && !done:
		return p, errors.New("--close goes with --done, for a task complete and cleaned up")
	case review:
		p.Settles = mark.Asking
	case done:
		p.Settles = mark.Finished
	}
	p.Text = strings.TrimSpace(strings.Join(fs.Args(), " "))
	if p.Text == "" && p.Type == "" {
		return p, errNoText
	}
	return p, p.Check(links)
}

func announceArgs(args []string, links []string) (post.Post, error) {
	var p post.Post
	fs := flag.NewFlagSet("announce", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Func("file", "", func(path string) error { p.Files = append(p.Files, path); return nil })
	fs.StringVar(&p.PR, "pr", "", "")
	if err := fs.Parse(args); err != nil {
		return p, err
	}
	if p.Text = strings.TrimSpace(strings.Join(fs.Args(), " ")); p.Text == "" {
		return p, errNoText
	}
	return p, p.Check(links)
}

func repoOf(server config.Server) post.Repos {
	return func(string) (string, error) { return origin.Of(server.Repo) }
}

func editArgs(args []string) (string, amend.Change, error) {
	var c amend.Change
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&c.DropFiles, "drop-files", false, "")
	fs.StringVar(&c.PR, "pr", "", "")
	if err := fs.Parse(args); err != nil {
		return "", c, err
	}
	if fs.NArg() == 0 {
		return "", c, errors.New("edit takes the link of the message to change")
	}
	text := fs.Args()[1:]
	// textStdin hands the text on after a "--", which flag parsing has already stopped before,
	// at the link.
	if len(text) > 0 && text[0] == "--" {
		text = text[1:]
	}
	if c.Text = strings.TrimSpace(strings.Join(text, " ")); c.Text == "" {
		return "", c, errNoText
	}
	return fs.Arg(0), c, nil
}

func deleteArgs(args []string) (string, error) {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return "", errors.New("delete takes the link of the message to take down, and nothing else")
	}
	return args[0], nil
}

// The server whose repository the working directory is a checkout of, found by origin rather
// than by MOCHI_THREAD, so a session mochi did not start can announce too; and the checkout's
// root.
func announcing(cfg config.Config) (config.Server, string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return config.Server{}, "", err
	}
	repos := make([]string, len(cfg.Servers))
	for i, s := range cfg.Servers {
		repos[i] = s.Repo
	}
	root, i, err := origin.Match(wd, repos)
	if err != nil {
		return config.Server{}, "", fmt.Errorf("announce runs in a checkout of a repository a server is mapped to: %v", err)
	}
	return cfg.Servers[i], root, nil
}

func splitArgs(s config.Split, args []string) (config.Split, error) {
	fs := flag.NewFlagSet("split-eval", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&s.Model, "model", s.Model, "")
	fs.StringVar(&s.Effort, "effort", s.Effort, "")
	if err := fs.Parse(args); err != nil {
		return s, err
	}
	if fs.NArg() > 0 {
		return s, fmt.Errorf("split-eval reads its cases on stdin, not %s", fs.Arg(0))
	}
	return s, s.Check()
}

func historyArgs(args []string) (history.Query, error) {
	q := history.Query{}
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&q.Before, "before", "", "")
	fs.StringVar(&q.After, "after", "", "")
	fs.StringVar(&q.Around, "around", "", "")
	fs.IntVar(&q.Limit, "limit", history.DefaultLimit, "")
	fs.BoolVar(&q.Post, "post", false, "")
	if err := fs.Parse(args); err != nil {
		return q, err
	}
	if fs.NArg() > 0 {
		return q, fmt.Errorf("history takes no %s: it reads only the conversation this session started from", fs.Arg(0))
	}
	return q, q.Check()
}
