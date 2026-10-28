// Package source reads the two Amazon reports into uniform line values.
// Every line keeps its file line number and its raw cells, so any number can
// be traced back to the exact input line. Malformed lines come out as
// ParseError values and reading continues.
package source

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Report names, as stored in source_rows.source.
const (
	Payments   = "payments"
	Settlement = "settlement"
)

// Line kinds, as stored in source_rows.line_kind.
const (
	KindTransaction      = "transaction"
	KindComponent        = "component"
	KindSettlementHeader = "settlement_header"
)

// Posted date bases, as stored in source_rows.posted_date_basis.
const (
	BasisReleaseUTC  = "release_date_utc" // payments Transaction Release Date, GMT+9 converted to UTC
	BasisDateTimeUTC = "date_time_utc"    // payments date/time (GMT+9 to UTC), used when there is no release date
	BasisPostedDate  = "posted_date"      // settlement posted-date
)

// Line is one normalised line from either report.
type Line struct {
	Source     string
	Kind       string
	LineNumber int
	Raw        map[string]string // header name to raw cell, exactly as provided

	SettlementID    string
	TxnType         string
	AmountType      string // components only
	Description     string
	TxnRef          string
	SKU             string
	Status          string // payments only
	Amount          decimal.Decimal
	PostedDate      time.Time // UTC date at midnight; zero when absent
	PostedDateBasis string

	// Settlement header only.
	Currency    string
	PeriodStart time.Time
	PeriodEnd   time.Time
	DepositDate time.Time
}

// ParseError is a line that could not be read. It carries what is needed to find it.
type ParseError struct {
	Source     string
	LineNumber int
	Raw        string
	Reason     string
}

func (e ParseError) Error() string {
	return fmt.Sprintf("%s line %d: %s", e.Source, e.LineNumber, e.Reason)
}

var (
	groupedAmount = regexp.MustCompile(`^-?(\d{1,3}(,\d{3})+|\d+)(\.\d+)?$`)
	plainAmount   = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	gmt9Date      = regexp.MustCompile(`^(\d{1,2}) ([A-Za-z]+) (\d{4}) (\d{1,2}:\d{2}:\d{2} (?:am|pm)) GMT\+9$`)
	gmt9Zone      = time.FixedZone("GMT+9", 9*60*60)
)

// parseGroupedAmount reads a payments amount, which may carry thousands separators
// (for example "-133,756.51"). The result is exact: there are no floats.
func parseGroupedAmount(s string) (decimal.Decimal, error) {
	if !groupedAmount.MatchString(s) {
		return decimal.Zero, fmt.Errorf("amount %q is not a number", s)
	}
	return decimal.NewFromString(strings.ReplaceAll(s, ",", ""))
}

// parsePlainAmount reads a settlement amount, which has no separators.
func parsePlainAmount(s string) (decimal.Decimal, error) {
	if !plainAmount.MatchString(s) {
		return decimal.Zero, fmt.Errorf("amount %q is not a number", s)
	}
	return decimal.NewFromString(s)
}

// checkTwoDecimals rejects amounts that the NUMERIC(14,2) column would round.
func checkTwoDecimals(d decimal.Decimal, s string) (decimal.Decimal, error) {
	if d.Exponent() < -2 {
		return decimal.Zero, fmt.Errorf("amount %q has more than 2 decimal places", s)
	}
	return d, nil
}

// parseGMT9Date reads a payments timestamp such as "3 July 2026 8:30:00 am GMT+9"
// and returns the UTC date it falls on, as midnight UTC.
func parseGMT9Date(s string) (time.Time, error) {
	m := gmt9Date.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, fmt.Errorf("date %q is not in the GMT+9 form", s)
	}
	// The report writes the month in full ("3 July") or abbreviated ("1 Aug").
	rest := m[1] + " " + m[2] + " " + m[3] + " " + m[4]
	t, err := time.ParseInLocation("2 January 2006 3:04:05 pm", rest, gmt9Zone)
	if err != nil {
		t, err = time.ParseInLocation("2 Jan 2006 3:04:05 pm", rest, gmt9Zone)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("date %q: %w", s, err)
	}
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC), nil
}

// parseDottedDate reads a settlement posted-date such as "17.07.2026".
func parseDottedDate(s string) (time.Time, error) {
	t, err := time.Parse("02.01.2006", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("date %q: %w", s, err)
	}
	return t, nil
}

// parseUTCStamp reads a settlement timestamp such as "17.07.2026 07:06:34 UTC".
func parseUTCStamp(s string) (time.Time, error) {
	t, err := time.Parse("02.01.2006 15:04:05 MST", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}
