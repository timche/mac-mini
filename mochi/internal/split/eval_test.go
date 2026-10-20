package split

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEvalScoresEachCaseThroughTheProductionCall(t *testing.T) {
	answers := []string{
		`{"is_error":false,"structured_output":{"tasks":[{"title":"A","instruction":"research a"},{"title":"B","instruction":"research b"}]}}`,
		`{"is_error":false,"structured_output":{"tasks":[{"title":"All","instruction":"research a and b"}]}}`,
		`not json`,
		`{"is_error":false,"structured_output":{"tasks":[` + strings.Repeat(`{"title":"x","instruction":"y"},`, 5) + `{"title":"x","instruction":"y"}]}}`,
	}
	var stdins []string
	var args [][]string
	s := Splitter{Model: "haiku", Effort: "low", Timeout: time.Second,
		Run: func(_ context.Context, stdin string, a []string) ([]byte, error) {
			stdins, args = append(stdins, stdin), append(args, a)
			return []byte(answers[len(stdins)-1]), nil
		}}
	clock := time.Unix(0, 0)
	now := func() time.Time { clock = clock.Add(time.Second); return clock }

	in := strings.NewReader(`{"instruction":"research a and b separately","replied":"a) elo b) maps","want":2}
{"instruction":"research a and b","want":2}
{"instruction":"research a","want":1}
{"instruction":"do six separately","want":6}
`)
	var out strings.Builder
	if err := Eval(in, &out, s, now); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("output:\n%s", out.String())
	}
	var results []Result
	for _, line := range lines[:4] {
		var r Result
		json.Unmarshal([]byte(line), &r)
		results = append(results, r)
	}
	if !results[0].Pass || results[0].Got != 2 || results[0].Seconds != 1 || !reflect.DeepEqual(results[0].Titles, []string{"A", "B"}) {
		t.Errorf("case 1 = %+v", results[0])
	}
	if results[1].Pass || results[1].Got != 1 {
		t.Errorf("case 2 = %+v", results[1])
	}
	if results[2].Pass || results[2].Error == "" {
		t.Errorf("case 3 = %+v", results[2])
	}
	if !results[3].Pass || results[3].Got != 6 {
		t.Errorf("case 4 = %+v", results[3])
	}

	var sum Summary
	json.Unmarshal([]byte(lines[4]), &sum)
	if sum != (Summary{Model: "haiku", Effort: "low", Cases: 4, Passed: 2, Seconds: 4}) {
		t.Errorf("summary = %+v", sum)
	}

	// Exactly what production sends.
	if !reflect.DeepEqual(args[0], Args("haiku", "low")) {
		t.Errorf("args = %q", args[0])
	}
	context := stdins[0][strings.Index(stdins[0], "----- BEGIN DISCORD CONTEXT"):]
	if !strings.Contains(stdins[0], "research a and b separately") || !strings.Contains(context, "a) elo b) maps") {
		t.Errorf("the prompt is not production's:\n%s", stdins[0])
	}
	if strings.Contains(stdins[1], "BEGIN DISCORD CONTEXT") {
		t.Error("a case with nothing replied to was handed an empty message")
	}
}

func TestEvalRefusesALineThatIsNotACase(t *testing.T) {
	s := Splitter{Run: func(context.Context, string, []string) ([]byte, error) { return nil, nil }}
	if err := Eval(strings.NewReader("{\"want\":1}\n"), &strings.Builder{}, s, time.Now); err == nil {
		t.Error("a case with no instruction was run")
	}
}
