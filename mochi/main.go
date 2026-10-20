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
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/config"
	"github.com/timche/mac-mini/mochi/internal/event"
	"github.com/timche/mac-mini/mochi/internal/history"
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
       mochi post [--review | --done] [--pr <url>] [--file <path>]... [<text>]
       mochi history [--before <id> | --after <id> | --around <id>] [--limit <n>]

  listen    connect to Discord and start a Claude Code session in herdr whenever Tim
            mentions mochi; what the LaunchAgent runs, and what no command means
  post      post into this session's Discord thread, with no pings; --file attaches
            a screenshot, a recording or any other file, and may be given up to
            ten times; --review for a result ready for Tim's review, with proof
            (❓), --done when the task is complete with nothing left for him (✅);
            --pr adds a pull request as "#<number> (<url>)", the one link allowed;
            with no text argument the text is read from stdin
  history   print more of the channel or thread this session started from, oldest
            first and fenced as data, with attachments downloaded; one of --before,
            --after or --around a message id, and --limit up to 100 (default 50)

post and history run only inside a session mochi started.
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
	if len(args) > 0 && args[0] == "version" {
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
		p, err := postArgs(args)
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
		return post.Send(api, state.Store{Path: cfg.StatePath()}, marks, repos, thread, p)

	// Left out of the usage: it is for choosing the model in [split], and reads cases rather
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

// REST alone, with no gateway: a post or a page of history is a request or two.
func rest() (chat.API, error) {
	token := os.Getenv(op.TokenVar)
	if token == "" {
		return nil, errors.New("the bot token did not resolve")
	}
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, errors.New(chat.Redact(err, token))
	}
	return chat.NewLive(s), nil
}

var errNoText = errors.New("post takes the text to post, as an argument or on stdin")

// With no text among the arguments, the text is stdin, so a post of several lines needs no
// shell quoting. Read here, before `op run`, and handed on as the last argument, since the
// process that runs under op cannot be given back a stdin this one has read.
func postStdin(args []string, stdin io.Reader) ([]string, error) {
	if _, err := postArgs(args); !errors.Is(err, errNoText) {
		return args, nil
	}
	body, err := io.ReadAll(io.LimitReader(stdin, 64<<10))
	if err != nil {
		return nil, err
	}
	return append(append([]string{}, args...), "--", string(body)), nil
}

func postArgs(args []string) (post.Post, error) {
	var p post.Post
	var review, done bool
	fs := flag.NewFlagSet("post", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Func("file", "", func(path string) error { p.Files = append(p.Files, path); return nil })
	fs.BoolVar(&review, "review", false, "")
	fs.BoolVar(&done, "done", false, "")
	fs.StringVar(&p.PR, "pr", "", "")
	if err := fs.Parse(args); err != nil {
		return p, err
	}
	switch {
	case review && done:
		return p, errors.New("post takes --review or --done, not both")
	case review:
		p.Settles = mark.Asking
	case done:
		p.Settles = mark.Finished
	}
	p.Text = strings.TrimSpace(strings.Join(fs.Args(), " "))
	if p.Text == "" {
		return p, errNoText
	}
	return p, p.Check()
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
	if err := fs.Parse(args); err != nil {
		return q, err
	}
	if fs.NArg() > 0 {
		return q, fmt.Errorf("history takes no %s: it reads only the conversation this session started from", fs.Arg(0))
	}
	return q, q.Check()
}
