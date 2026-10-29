package discord

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// A post as Discord describes one in the forum's closed listing.
func post(id, name, tag string, archived bool, closed time.Time) string {
	body, err := json.Marshal(map[string]any{
		"id":           id,
		"name":         name,
		"type":         11,
		"parent_id":    "forum",
		"applied_tags": []string{tag},
		"thread_metadata": map[string]any{
			"archived":          archived,
			"archive_timestamp": closed.Format(time.RFC3339),
		},
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}

const forumWithTags = `{"id":"forum","type":15,"available_tags":[` +
	`{"id":"tag-open","name":"Open"},{"id":"tag-resolved","name":"Resolved"}]}`

// Deleting a post cannot be undone, so what may go is narrow and every part of it is read
// off Discord: in the forum, tagged Resolved, archived, and archived longer ago than the
// age it is kept for.
func TestOnlyPostsResolvedLongEnoughAgoAreDeleted(t *testing.T) {
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	old := now.Add(-4 * 24 * time.Hour)
	recent := now.Add(-2 * time.Hour)

	listing := fmt.Sprintf(`{"threads":[%s,%s,%s,%s],"has_more":false}`,
		post("gone", "Disk filling", "tag-resolved", true, old),
		post("open", "CPU busy", "tag-open", false, old),
		post("reopened", "Somebody wrote in it", "tag-resolved", false, old),
		post("fresh", "Resolved this morning", "tag-resolved", true, recent))

	var deleted []string
	bot, _ := fakeBotWith(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			deleted = append(deleted, strings.TrimPrefix(r.URL.Path, "/channels/"))
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/threads/archived/public"):
			w.Write([]byte(listing))
		default:
			w.Write([]byte(forumWithTags))
		}
	})

	var out, errs bytes.Buffer
	if err := prune(bot, "forum", 3*24*time.Hour, now, &out, &errs); err != nil {
		t.Fatal(err)
	}

	harness.Equal(t, strings.Join(deleted, ","), "gone", "the posts deleted")
	harness.Wants(t, out.String(), `deleted the resolved post "Disk filling", closed 2026-10-13`)
	harness.Equal(t, errs.String(), "", "what it could not do")
}

// A forum with no Resolved tag is one where nothing can be read as resolved, and a text
// channel has no posts at all. Neither is a reason to guess.
func TestAForumWithNoResolvedTagLosesNothing(t *testing.T) {
	for _, channel := range []string{
		`{"id":"forum","type":15,"available_tags":[{"id":"tag-open","name":"Open"}]}`,
		`{"id":"forum","type":0}`,
	} {
		asked := 0
		bot, _ := fakeBotWith(t, func(w http.ResponseWriter, r *http.Request) {
			asked++
			if r.Method == http.MethodDelete {
				t.Errorf("it deleted %s", r.URL.Path)
			}
			w.Write([]byte(channel))
		})

		var out, errs bytes.Buffer
		if err := prune(bot, "forum", time.Hour, base, &out, &errs); err != nil {
			t.Fatal(err)
		}
		harness.Equal(t, asked, 1, "calls made")
	}
}

// A post Discord refuses to delete is one it will refuse again in the same way, so it is
// said once and the rest of the page is still cleared.
func TestAPostThatWillNotGoIsSaidRatherThanRetried(t *testing.T) {
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	old := now.Add(-4 * 24 * time.Hour)

	listing := fmt.Sprintf(`{"threads":[%s,%s],"has_more":false}`,
		post("stuck", "Will not go", "tag-resolved", true, old),
		post("gone", "Goes", "tag-resolved", true, old))

	tries := 0
	bot, _ := fakeBotWith(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "stuck"):
			tries++
			w.WriteHeader(http.StatusForbidden)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/threads/archived/public"):
			w.Write([]byte(listing))
		default:
			w.Write([]byte(forumWithTags))
		}
	})

	var out, errs bytes.Buffer
	if err := prune(bot, "forum", 3*24*time.Hour, now, &out, &errs); err != nil {
		t.Fatal(err)
	}

	harness.Equal(t, tries, 1, "attempts at the post that would not go")
	harness.Wants(t, errs.String(), "the resolved post stuck could not be deleted")
	harness.Wants(t, out.String(), `deleted the resolved post "Goes"`)
}

// The listing is paged by the timestamp to continue from, so a forum with more than a page
// of closed posts is read to the end rather than one page deep.
func TestTheClosedListingIsPagedThrough(t *testing.T) {
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	first := now.Add(-4 * 24 * time.Hour)
	second := now.Add(-5 * 24 * time.Hour)

	var queries []string
	bot, _ := fakeBotWith(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/threads/archived/public"):
			queries = append(queries, r.URL.Query().Get("before"))
			if len(queries) == 1 {
				fmt.Fprintf(w, `{"threads":[%s],"has_more":true}`,
					post("one", "First page", "tag-resolved", true, first))
				return
			}
			fmt.Fprintf(w, `{"threads":[%s],"has_more":false}`,
				post("two", "Second page", "tag-resolved", true, second))
		default:
			w.Write([]byte(forumWithTags))
		}
	})

	var out, errs bytes.Buffer
	if err := prune(bot, "forum", 3*24*time.Hour, now, &out, &errs); err != nil {
		t.Fatal(err)
	}

	harness.Equal(t, len(queries), 2, "pages read")
	harness.Equal(t, queries[1], first.Format(time.RFC3339), "where the second page started")
	harness.Wants(t, out.String(), "First page")
	harness.Wants(t, out.String(), "Second page")
}
