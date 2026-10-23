package automation

import (
	"errors"
	"time"

	"github.com/robfig/cron/v3"
)

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// ErrNoFutureRun means a syntactically valid expression never fires again.
var ErrNoFutureRun = errors.New("Cron expression has no future occurrence in its timezone")

// ValidCron is the syntax-only check run on input.
func ValidCron(expr string) bool {
	_, err := cronParser.Parse(expr)
	return err == nil
}

// LoadZone resolves an IANA timezone name.
func LoadZone(name string) (*time.Location, error) {
	if name == "" {
		return nil, errors.New("Unknown timezone")
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, errors.New("Unknown timezone")
	}
	return loc, nil
}

// NextRun evaluates expr in the automation's own zone so "daily at 9am"
// follows the user's wall clock (incl. DST), and returns the UTC instant.
func NextRun(expr, tzName string, base time.Time) (time.Time, error) {
	sched, err := cronParser.Parse(expr)
	if err != nil {
		return time.Time{}, err
	}
	loc, err := LoadZone(tzName)
	if err != nil {
		return time.Time{}, err
	}
	base = base.In(loc)
	next := sched.Next(base)
	// DST fall-back: during the repeated hour a candidate's wall-clock time can
	// precede base's even though the instant is later. Fire each wall-clock
	// slot at most once.
	for !next.IsZero() && !wallClock(next).After(wallClock(base)) {
		next = sched.Next(next)
	}
	if next.IsZero() {
		return time.Time{}, ErrNoFutureRun
	}
	return next.UTC(), nil
}

func wallClock(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}
