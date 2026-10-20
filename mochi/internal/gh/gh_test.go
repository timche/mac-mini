package gh

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func answering(body string) Runner {
	return func(args ...string) ([]byte, error) { return []byte(body), nil }
}

func TestAPullRequestIsReadAsGitHubAnswersIt(t *testing.T) {
	var asked []string
	run := func(args ...string) ([]byte, error) {
		asked = args
		return []byte(`{"state":"closed","merged":true,"number":7,"head":{"sha":"abc"}}`), nil
	}
	pr, err := Pull(run, "timche/repeek", 7)
	if err != nil || pr != (PR{Open: false, Merged: true, Head: "abc"}) {
		t.Errorf("pr = %+v, %v", pr, err)
	}
	if strings.Join(asked, " ") != "api repos/timche/repeek/pulls/7" {
		t.Errorf("asked %q", asked)
	}
	if _, err := Pull(answering(`{"message":"Not Found"}`), "timche/repeek", 7); err == nil {
		t.Error("an answer with no state was taken for a pull request")
	}
	if _, err := Pull(func(...string) ([]byte, error) { return nil, errors.New("offline") }, "timche/repeek", 7); err == nil {
		t.Error("a failed call was not an error")
	}
}

// Only finished runs that failed count, sorted by name.
func TestOnlyFailedRunsCount(t *testing.T) {
	names, err := Failed(answering(`{"check_runs":[
		{"name":"test","status":"completed","conclusion":"failure"},
		{"name":"lint","status":"completed","conclusion":"success"},
		{"name":"skip","status":"completed","conclusion":"skipped"},
		{"name":"e2e","status":"in_progress","conclusion":null},
		{"name":"build","status":"completed","conclusion":"timed_out"}]}`), "timche/repeek", "abc")
	if err != nil || !reflect.DeepEqual(names, []string{"build", "test"}) {
		t.Errorf("failed = %v, %v", names, err)
	}
}
