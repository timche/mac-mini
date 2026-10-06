package main

import (
	"fmt"
	"math"
	"time"
)

func gbStr(kb int64) string { return fmt.Sprintf("%.1f", float64(kb)/gib) }

// The same number gbStr prints, as a number: shibuya is handed it as JSON and writes the
// message about it itself, so the rounding has to happen before it leaves.
func gbNum(kb int64) float64 { return math.Round(float64(kb)/gib*10) / 10 }

func mbStr(kb int64) string { return fmt.Sprintf("%.0f", float64(kb)/1024) }

func hmStr(d time.Duration) string {
	s := int64(d.Seconds())
	if s < 0 {
		s = 0
	}
	return fmt.Sprintf("%dh%02dm", s/3600, (s%3600)/60)
}

// GB an hour, from a growth and the span it happened over, which is the number
// that says whether a file is a nuisance or an emergency.
func rateStr(grewKB int64, span time.Duration) string {
	seconds := span.Seconds()
	if seconds <= 0 {
		seconds = 1
	}
	return fmt.Sprintf("%.1f", float64(grewKB)/gib*3600/seconds)
}
