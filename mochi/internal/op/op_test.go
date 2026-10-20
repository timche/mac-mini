package op

import (
	"reflect"
	"testing"
)

func TestAChildInheritsNoSecret(t *testing.T) {
	got := Scrub([]string{
		"HOME=/home/x",
		"MOCHI_DISCORD_BOT_TOKEN=secret",
		"OP_SERVICE_ACCOUNT_TOKEN=ops_secret",
		"OP_SESSION_abc=x",
		"MOCHI_THREAD=123",
		"PATH=/bin",
	})
	want := []string{"HOME=/home/x", "MOCHI_THREAD=123", "PATH=/bin"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scrubbed to %v", got)
	}
}
