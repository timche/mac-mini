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

	"github.com/bwmarrin/discordgo"

	"github.com/timche/mac-mini/mochi/internal/chat"
	"github.com/timche/mac-mini/mochi/internal/config"
	"github.com/timche/mac-mini/mochi/internal/history"
	"github.com/timche/mac-mini/mochi/internal/listen"
	"github.com/timche/mac-mini/mochi/internal/op"
	"github.com/timche/mac-mini/mochi/internal/post"
	"github.com/timche/mac-mini/mochi/internal/self"
	"github.com/timche/mac-mini/mochi/internal/state"
)

const usage = `usage: mochi [listen]
       mochi post [--file <path>] <text>
       mochi history [--before <id> | --after <id> | --around <id>] [--limit <n>]

  listen    connect to Discord and start a Claude Code session in herdr whenever Tim
            mentions mochi; what the LaunchAgent runs, and what no command means
  post      post into this session's Discord thread, with no pings; --file attaches
            a screenshot or any other file
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
		file, text, err := postArgs(args)
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
		return post.Send(api, state.Store{Path: cfg.StatePath()}, thread, file, text)

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

func postArgs(args []string) (file, text string, err error) {
	fs := flag.NewFlagSet("post", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&file, "file", "", "")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	text = strings.TrimSpace(strings.Join(fs.Args(), " "))
	if text == "" {
		return "", "", errors.New("post takes the text to post")
	}
	if file != "" {
		if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
			return "", "", fmt.Errorf("%s is not a file", file)
		}
	}
	return file, text, nil
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
