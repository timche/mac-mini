package split

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/bwmarrin/discordgo"
)

// One case: what Tim wrote, the message he replied to, and how many sessions it should start.
type Case struct {
	Instruction string `json:"instruction"`
	Replied     string `json:"replied"`
	Want        int    `json:"want"`
}

type Result struct {
	Case    int      `json:"case"`
	Want    int      `json:"want"`
	Got     int      `json:"got"`
	Seconds float64  `json:"seconds"`
	Pass    bool     `json:"pass"`
	Titles  []string `json:"titles,omitempty"`
	Error   string   `json:"error,omitempty"`
}

type Summary struct {
	Model   string  `json:"model"`
	Effort  string  `json:"effort"`
	Cases   int     `json:"cases"`
	Passed  int     `json:"passed"`
	Seconds float64 `json:"seconds"`
}

// `mochi split-eval`: the cases as JSON lines in, a result per case and a summary out, through
// the same prompt, schema, flags and parsing as a mention. A case over the cap counts what
// claude returned, so a case wanting six passes on six.
func Eval(in io.Reader, out io.Writer, s Splitter, now func() time.Time) error {
	enc := json.NewEncoder(out)
	sum := Summary{Model: s.Model, Effort: s.Effort}

	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 1<<20), 1<<20)
	for n := 1; scan.Scan(); n++ {
		var c Case
		if err := json.Unmarshal(scan.Bytes(), &c); err != nil || c.Instruction == "" {
			return fmt.Errorf("line %d is not a case with an instruction", n)
		}

		var replied *discordgo.Message
		if c.Replied != "" {
			replied = &discordgo.Message{Content: c.Replied}
		}
		start := now()
		tasks, err := s.Split(c.Instruction, replied)
		r := Result{Case: n, Want: c.Want, Seconds: now().Sub(start).Seconds()}

		var tooMany TooMany
		switch {
		case errors.As(err, &tooMany):
			r.Got = tooMany.N
		case err != nil:
			r.Error = err.Error()
		default:
			r.Got = len(tasks)
			for _, t := range tasks {
				r.Titles = append(r.Titles, t.Title)
			}
		}
		r.Pass = r.Error == "" && r.Got == c.Want

		sum.Cases++
		sum.Seconds += r.Seconds
		if r.Pass {
			sum.Passed++
		}
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	return enc.Encode(sum)
}
