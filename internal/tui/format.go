package tui

import (
	"fmt"
	"math"
	"strconv"
	"time"
)

func fmtNum(f float64) string {
	return strconv.FormatFloat(math.Round(f*100)/100, 'f', -1, 64)
}

// fmtWhen drops the year for dates in the current year.
func fmtWhen(t time.Time) string {
	t = t.Local()
	if t.Year() == time.Now().Year() {
		return t.Format("Mon Jan 2 15:04")
	}
	return t.Format("Mon Jan 2 2006")
}

// relTime renders t relative to now, e.g. "in 3d" or "2h ago".
func relTime(t, now time.Time) string {
	d := t.Sub(now)
	past := d < 0
	if past {
		d = -d
	}
	var s string
	switch {
	case d < time.Hour:
		s = fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		s = fmt.Sprintf("%dh", int(d.Hours()))
	default:
		s = fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	if past {
		return s + " ago"
	}
	return "in " + s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
