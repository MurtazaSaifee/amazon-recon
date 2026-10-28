package source

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
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
	if err := skipBOM(br); err != nil {
		return out, err
	}

	lineNo := 0
	for range paymentsPreambleLines {
		text, ok, err := readLine(br)
		if err != nil {
			return out, err
		}
		lineNo++
		if !ok {
			return out, errors.New("payments report ends inside its preamble")
		}
		out.Preamble = append(out.Preamble, text)
	}

	text, ok, err := readLine(br)
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
		text, ok, err := readLine(br)
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
		line, err := parsePaymentsLine(lineNo, text, header)
		if err != nil {
			out.Errors = append(out.Errors, ParseError{Source: Payments, LineNumber: lineNo, Raw: text, Reason: err.Error()})
			continue
		}
		out.Lines = append(out.Lines, line)
	}
	return out, nil
}

func parsePaymentsLine(lineNo int, text string, header []string) (Line, error) {
	rec, err := parseCSVRecord(text)
	if err != nil {
		return Line{}, err
	}
	if len(rec) != len(header) {
		return Line{}, fmt.Errorf("expected %d columns, found %d", len(header), len(rec))
	}
	raw := make(map[string]string, len(header))
	for i, h := range header {
		raw[h] = rec[i]
	}
	if raw["settlement ID"] == "" {
		return Line{}, errors.New("settlement ID is empty")
	}
	if raw["Transaction status"] == "" {
		return Line{}, errors.New("Transaction status is empty")
	}
	amount, err := ParseAmount(raw["total"])
	if err != nil {
		return Line{}, fmt.Errorf("total: %w", err)
	}
	for _, col := range PaymentsAmountColumns {
		if _, err := ParseAmount(raw[col]); err != nil {
			return Line{}, fmt.Errorf("%s: %w", col, err)
		}
	}

	// Released lines carry a release date, which is the posted date. Deferred
	// lines have none, so they fall back to date/time. Both are GMT+9.
	when, basis := raw["Transaction Release Date"], BasisReleaseUTC
	if when == "" {
		when, basis = raw["date/time"], BasisDateTimeUTC
	}
	posted, err := parseGMT9Date(when)
	if err != nil {
		return Line{}, err
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
		PostedDate:      posted,
		PostedDateBasis: basis,
	}, nil
}

// skipBOM drops a leading UTF-8 byte order mark, if there is one.
func skipBOM(br *bufio.Reader) error {
	b, err := br.Peek(3)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if bytes.Equal(b, []byte{0xEF, 0xBB, 0xBF}) {
		_, err = br.Discard(3)
		return err
	}
	return nil
}

// readLine returns the next line without its line ending. ok is false at the
// end of input. A final line without a newline is still returned.
func readLine(br *bufio.Reader) (text string, ok bool, err error) {
	s, err := br.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, err
	}
	if s == "" {
		return "", false, nil
	}
	return strings.TrimRight(s, "\r\n"), true, nil
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
