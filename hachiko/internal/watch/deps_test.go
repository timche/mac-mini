package watch

import (
	"testing"
	"time"
)

func TestBootedReadsWhenThisMacStarted(t *testing.T) {
	boot := booted()
	if boot.IsZero() || boot.After(time.Now()) || boot.Before(time.Now().Add(-365*24*time.Hour)) {
		t.Fatalf("booted() = %v, which is not a boot this year", boot)
	}
}
