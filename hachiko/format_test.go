package main

import (
	"testing"
	"time"
)

func TestSizesReadAsTheNumbersTheyAreNamedFor(t *testing.T) {
	equal(t, gbStr(100*gib), "100.0", "a hundred gibibytes")
	equal(t, gbStr(20*gib), "20.0", "twenty gibibytes")
	equal(t, mbStr(524288), "512", "half a gibibyte of resident memory")
	equal(t, hmStr(3600*time.Second), "1h00m", "an hour")
	equal(t, hmStr(5*3600*time.Second+90*time.Second), "5h01m", "five hours and a bit")
}

// 90 GB an hour is what the worker that caused this managed, and the rate is what says
// whether a file is a nuisance or an emergency.
func TestTheRateIsGigabytesAnHourWhateverTheInterval(t *testing.T) {
	equal(t, rateStr(7500*1024, 300*time.Second), "87.9", "7.5 GB in five minutes")
	equal(t, rateStr(2*gib, 3600*time.Second), "2.0", "2 GB in an hour")
	equal(t, rateStr(gib, 0), "3600.0", "a span that cannot be zero")
}

func TestClipKeepsACommandLineReadable(t *testing.T) {
	short := "/usr/local/bin/node worker.js"
	equal(t, clip(short, 200), short, "a short command")

	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	equal(t, len(clip(string(long), 200)), 203, "a clipped command")
}
