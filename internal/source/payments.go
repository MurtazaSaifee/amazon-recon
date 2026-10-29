package source

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"amazonrecon/internal/textfile"
)

const paymentsPreambleLines = 9

// paymentsHeader is the exact header of the payments report (line 10 of the file).
var paymentsHeader = []string{
	"date/time", "settlement ID", "type", "order ID", "sku", "description", "quantity", "marketplace",
	"fulfilment", "order city", "order state", "order postal", "product sales", "shipping credits",
	"gift wrap credits", "promotional rebates", "sales tax collected", "low value goods", "selling fees",
	"fulfilment by amazon fees", "other transaction fees", "other", "total", "Transaction status",
	"Transaction Release Date",
}

// PaymentsAmountColumns are the typed amount columns of a Transaction line, other
// than total. Each one is an Amount field a mapping rule can read.
var PaymentsAmountColumns = []string{
	"product sales", "shipping credits", "gift wrap credits", "promotional rebates", "sales tax collected",
	"low value goods", "selling fees", "fulfilment by amazon fees", "other transaction fees", "other",
}

// PaymentsReport is the result of reading the payments report.
type PaymentsReport struct {
	Preamble []string     // the 9 lines before the header, kept as run metadata
	Lines    []Line       // transaction lines, in file order
	Errors   []ParseError // malformed lines
}

// ReadPayments reads the payments report. It strips the UTF-8 BOM, keeps the
// preamble, and parses each line on its own, so a malformed line is reported
// with its exact line number and raw text. A header that is not the expected
// shape is a file-level error.
func ReadPayments(r io.Reader) (PaymentsReport, error) {
	br := bufio.NewReader(r)
	var out PaymentsReport
	if err := textfile.SkipBOM(br); err != nil {
		return out, err
	}

	lineNo := 0
	for range paymentsPreambleLines {
		text, ok, err := textfile.ReadLine(br)
		if err != nil {
			return out, err
		}
		lineNo++
		if !ok {
			return out, errors.New("payments report ends inside its preamble")
		}
		out.Preamble = append(out.Preamble, text)
	}

	text, ok, err := textfile.ReadLine(br)
	if err != nil {
		return out, err
	}
	lineNo++
	if !ok {
		return out, errors.New("payments report has no header")
	}
	header, err := parseCSVRecord(text)
	if err != nil {
		return out, fmt.Errorf("payments header: %w", err)
	}
	if !slices.Equal(header, paymentsHeader) {
		return out, fmt.Errorf("payments header does not match the expected report shape")
	}

	for {
		text, ok, err := textfile.ReadLine(br)
		if err != nil {
			return out, err
		}
		if !ok {
			break
		}
		lineNo++
		if strings.TrimSpace(text) == "" {
			out.Errors = append(out.Errors, ParseError{Source: Payments, LineNumber: lineNo, Raw: text, Reason: "blank line"})
			continue
		}
		line, perr := parsePaymentsLine(lineNo, text, header)
		if perr != nil {
			out.Errors = append(out.Errors, *perr)
			continue
		}
		out.Lines = append(out.Lines, line)
	}
	return out, nil
}

// parsePaymentsLine reads one Transaction line. On failure it returns a ParseError that
// carries the settlement ID and status cells, when they were readable. A line whose column
// count is wrong has no reliable cell positions, so it carries neither.
func parsePaymentsLine(lineNo int, text string, header []string) (Line, *ParseError) {
	var settlementID, status string
	fail := func(reason string) (Line, *ParseError) {
		return Line{}, &ParseError{Source: Payments, LineNumber: lineNo, Raw: text, Reason: reason,
			SettlementID: settlementID, Status: status}
	}

	rec, err := parseCSVRecord(text)
	if err != nil {
		return fail(err.Error())
	}
	if len(rec) != len(header) {
		return fail(fmt.Sprintf("expected %d columns, found %d", len(header), len(rec)))
	}
	raw := make(map[string]string, len(header))
	for i, h := range header {
		raw[h] = rec[i]
	}
	settlementID, status = raw["settlement ID"], raw["Transaction status"]
	if settlementID == "" {
		return fail("settlement ID is empty")
	}
	if status == "" {
		return fail("Transaction status is empty")
	}
	amount, err := parsePaymentsAmount(raw["total"])
	if err != nil {
		return fail(fmt.Sprintf("total: %v", err))
	}
	amounts := make(map[string]decimal.Decimal, len(PaymentsAmountColumns))
	for _, col := range PaymentsAmountColumns {
		d, err := parsePaymentsAmount(raw[col])
		if err != nil {
			return fail(fmt.Sprintf("%s: %v", col, err))
		}
		amounts[col] = d
	}

	// Released lines carry a release date, which is the posted date. Deferred
	// lines have none, so they fall back to date/time. Both are GMT+9.
	when, basis := raw["Transaction Release Date"], BasisReleaseUTC
	if when == "" {
		when, basis = raw["date/time"], BasisDateTimeUTC
	}
	posted, err := parseGMT9Date(when)
	if err != nil {
		return fail(err.Error())
	}

	return Line{
		Source:          Payments,
		Kind:            KindTransaction,
		LineNumber:      lineNo,
		Raw:             raw,
		SettlementID:    raw["settlement ID"],
		TxnType:         raw["type"],
		Description:     raw["description"],
		TxnRef:          raw["order ID"],
		SKU:             raw["sku"],
		Status:          raw["Transaction status"],
		Amount:          amount,
		Amounts:         amounts,
		PostedDate:      posted,
		PostedDateBasis: basis,
	}, nil
}

// parseCSVRecord parses one physical line as one CSV record.
func parseCSVRecord(text string) ([]string, error) {
	cr := csv.NewReader(strings.NewReader(text))
	cr.FieldsPerRecord = -1
	rec, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("not a valid CSV record: %w", err)
	}
	return rec, nil
}
