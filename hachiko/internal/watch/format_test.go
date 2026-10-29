package watch

import (
	"testing"
	"time"

	"github.com/timche/mac-mini/hachiko/internal/config"
	"github.com/timche/mac-mini/hachiko/internal/harness"
	"github.com/timche/mac-mini/hachiko/internal/wording"
)

func TestSizesReadAsTheNumbersTheyAreNamedFor(t *testing.T) {
	harness.Equal(t, gbStr(100*config.GiB), "100.0", "a hundred gibibytes")
	harness.Equal(t, gbStr(20*config.GiB), "20.0", "twenty gibibytes")
	harness.Equal(t, mbStr(524288), "512", "half a gibibyte of resident memory")
	harness.Equal(t, hmStr(3600*time.Second), "1h00m", "an hour")
	harness.Equal(t, hmStr(5*3600*time.Second+90*time.Second), "5h01m", "five hours and a bit")
}

func TestClipKeepsACommandLineReadable(t *testing.T) {
	short := "/usr/local/bin/node worker.js"
	harness.Equal(t, wording.Clip(short, 200), short, "a short command")

	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	harness.Equal(t, len(wording.Clip(string(long), 200)), 203, "a clipped command")
}
