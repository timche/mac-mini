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

// One case: what Tim wrote, the message he replied to, how many sessions it should start, and
// for a case where the right answer is to ask him, Decision "ask".
type Case struct {
	Instruction string `json:"instruction"`
	Replied     string `json:"replied"`
	Want        int    `json:"want"`
	Decision    string `json:"decision,omitempty"`

	// The type the work as a whole should be filed under, for a case labelled with one.
	Type string `json:"type,omitempty"`
}

type Result struct {
	Case         int      `json:"case"`
	Want         int      `json:"want"`
	WantDecision string   `json:"want_decision,omitempty"`
	Decision     string   `json:"decision,omitempty"`
	Got          int      `json:"got"`
	Seconds      float64  `json:"seconds"`
	Pass         bool     `json:"pass"`
	Title        string   `json:"title,omitempty"`
	Titles       []string `json:"titles,omitempty"`
	WantType     string   `json:"want_type,omitempty"`
	Type         string   `json:"type,omitempty"`
	Types        []string `json:"types,omitempty"`
	TypePass     bool     `json:"type_pass,omitempty"`
	Error        string   `json:"error,omitempty"`
}

type Summary struct {
	Model   string  `json:"model"`
	Effort  string  `json:"effort"`
	Cases   int     `json:"cases"`
	Passed  int     `json:"passed"`
	Seconds float64 `json:"seconds"`

	// Of the cases labelled with a type, how many were filed under it; scored apart from
	// Passed, which stays the decision and the count.
	Typed      int `json:"typed"`
	TypePassed int `json:"type_passed"`
}

// `mochi split-eval`: the cases as JSON lines in, a result per case and a summary out, through
// the same prompt, schema, flags and parsing as a mention. Got is the sessions the decision
// would start, or for "ask" the items it would offer; a case over the cap counts what claude
// returned, so a case wanting six passes on six. A case with a Decision passes on that
// decision; any other passes on its count, and an "ask" for it is a miss.
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
		answer, err := s.Split(c.Instruction, replied)
		r := Result{Case: n, Want: c.Want, WantDecision: c.Decision, WantType: c.Type, Seconds: now().Sub(start).Seconds()}

		var tooMany TooMany
		switch {
		case errors.As(err, &tooMany):
			r.Decision, r.Got = Many, tooMany.N
		case err != nil:
			r.Error = err.Error()
		default:
			r.Decision, r.Title, r.Type = answer.Decision, answer.Title, answer.Type
			r.Got = len(answer.Tasks)
			if answer.Decision == One {
				r.Got = 1
			}
			for _, t := range answer.Tasks {
				r.Titles = append(r.Titles, t.Title)
				r.Types = append(r.Types, t.Type)
			}
		}
		if c.Type != "" {
			sum.Typed++
			if r.TypePass = r.Error == "" && r.Type == c.Type; r.TypePass {
				sum.TypePassed++
			}
		}
		switch {
		case r.Error != "":
		case c.Decision != "":
			r.Pass = r.Decision == c.Decision
		default:
			r.Pass = r.Decision != Ask && r.Got == c.Want
		}

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
