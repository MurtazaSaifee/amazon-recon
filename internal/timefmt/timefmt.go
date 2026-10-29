// Package timefmt formats the instants the reports label as UTC. A time read from PostgreSQL comes back
// in the local zone, so every value written with a UTC label goes through this package, which converts
// it to UTC first.
package timefmt

import "time"

// DateLayout is the layout of a UTC date, as the reports print it.
const DateLayout = "2006-01-02"

// TimestampLayout is the layout of a UTC timestamp, with its zone name.
const TimestampLayout = "2006-01-02 15:04:05 MST"

// Date returns the UTC calendar date of t, as YYYY-MM-DD.
func Date(t time.Time) string {
	return t.UTC().Format(DateLayout)
}

// Timestamp returns t in UTC, as "2006-01-02 15:04:05 UTC".
func Timestamp(t time.Time) string {
	return t.UTC().Format(TimestampLayout)
}
