package split

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A case that should be asked about passes on "ask" alone; a clear case asked about is a miss.
func TestEvalScoresTheDecision(t *testing.T) {
	answers := []string{
		`{"is_error":false,"structured_output":{"decision":"ask","title":"Suggestions","tasks":[{"title":"A","instruction":"a"},{"title":"B","instruction":"b"},{"title":"C","instruction":"c"}]}}`,
		`{"is_error":false,"structured_output":{"decision":"ask","title":"Suggestions","tasks":[{"title":"A","instruction":"a"},{"title":"B","instruction":"b"}]}}`,
	}
	n := 0
	s := Splitter{Model: "haiku", Effort: "medium", Timeout: time.Second,
		Run: func(context.Context, string, []string) ([]byte, error) { n++; return []byte(answers[n-1]), nil }}

	var out strings.Builder
	in := strings.NewReader(`{"instruction":"take a look at the three suggestions","want":3,"decision":"ask"}
{"instruction":"research a and b separately","want":2}
`)
	if err := Eval(in, &out, s, time.Now); err != nil {
		t.Fatal(err)
	}
	var first, second Result
	lines := strings.Split(out.String(), "\n")
	json.Unmarshal([]byte(lines[0]), &first)
	json.Unmarshal([]byte(lines[1]), &second)
	if !first.Pass || first.Decision != Ask || first.WantDecision != Ask || first.Got != 3 {
		t.Errorf("the ask case = %+v", first)
	}
	if second.Pass || second.Decision != Ask {
		t.Errorf("an ask on a clear case passed: %+v", second)
	}
}
