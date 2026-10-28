package source

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// settlementHeader is the exact header row of the settlement report (line 1).
var settlementHeader = []string{
	"settlement-id", "settlement-start-date", "settlement-end-date", "deposit-date", "total-amount",
	"currency", "transaction-type", "order-id", "merchant-order-id", "adjustment-id", "shipment-id",
	"marketplace-name", "amount-type", "amount-description", "amount", "fulfillment-id", "posted-date",
	"posted-date-time", "order-item-code", "merchant-order-item-id", "merchant-adjustment-item-id", "sku",
	"quantity-purchased", "promotion-id",
}

// SettlementReport is the result of reading the settlement report.
type SettlementReport struct {
	Header     Line         // the settlement header line (carries the Settlement total)
	Components []Line       // component lines, in file order
	Errors     []ParseError // malformed lines
}

// ReadSettlement reads the settlement report. The report has one header line
// that carries the Settlement total; every other line is a component. A file
// with no header, or with more than one, is a file-level error.
func ReadSettlement(r io.Reader) (SettlementReport, error) {
	br := bufio.NewReader(r)
	var out SettlementReport
	if err := skipBOM(br); err != nil {
		return out, err
	}

	text, ok, err := readLine(br)
	if err != nil {
		return out, err
	}
	if !ok {
		return out, errors.New("settlement report is empty")
	}
	header := strings.Split(text, "\t")
	if !slices.Equal(header, settlementHeader) {
		return out, errors.New("settlement header row does not match the expected report shape")
	}

	var headers []Line
	lineNo := 1
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
			out.Errors = append(out.Errors, ParseError{Source: Settlement, LineNumber: lineNo, Raw: text, Reason: "blank line"})
			continue
		}
		cells := strings.Split(text, "\t")
		if len(cells) != len(header) {
			out.Errors = append(out.Errors, ParseError{Source: Settlement, LineNumber: lineNo, Raw: text,
				Reason: fmt.Sprintf("expected %d columns, found %d", len(header), len(cells))})
			continue
		}
		raw := make(map[string]string, len(header))
		for i, h := range header {
			raw[h] = cells[i]
		}
		if raw["total-amount"] != "" {
			line, err := parseSettlementHeader(lineNo, raw)
			if err != nil {
				out.Errors = append(out.Errors, ParseError{Source: Settlement, LineNumber: lineNo, Raw: text, Reason: err.Error(),
					SettlementID: raw["settlement-id"]})
				continue
			}
			headers = append(headers, line)
			continue
		}
		line, err := parseSettlementComponent(lineNo, raw)
		if err != nil {
			out.Errors = append(out.Errors, ParseError{Source: Settlement, LineNumber: lineNo, Raw: text, Reason: err.Error(),
				SettlementID: raw["settlement-id"]})
			continue
		}
		out.Components = append(out.Components, line)
	}

	switch len(headers) {
	case 0:
		return out, errors.New("settlement report has no settlement header line")
	case 1:
		out.Header = headers[0]
	default:
		return out, fmt.Errorf("settlement report has %d settlement header lines, expected 1", len(headers))
	}
	return out, nil
}

func parseSettlementHeader(lineNo int, raw map[string]string) (Line, error) {
	if raw["settlement-id"] == "" {
		return Line{}, errors.New("settlement-id is empty")
	}
	total, err := parsePlainAmount(raw["total-amount"])
	if err != nil {
		return Line{}, fmt.Errorf("total-amount: %w", err)
	}
	if _, err := checkTwoDecimals(total, raw["total-amount"]); err != nil {
		return Line{}, fmt.Errorf("total-amount: %w", err)
	}
	start, err := parseUTCStamp(raw["settlement-start-date"])
	if err != nil {
		return Line{}, fmt.Errorf("settlement-start-date: %w", err)
	}
	end, err := parseUTCStamp(raw["settlement-end-date"])
	if err != nil {
		return Line{}, fmt.Errorf("settlement-end-date: %w", err)
	}
	deposit, err := parseUTCStamp(raw["deposit-date"])
	if err != nil {
		return Line{}, fmt.Errorf("deposit-date: %w", err)
	}
	return Line{
		Source:       Settlement,
		Kind:         KindSettlementHeader,
		LineNumber:   lineNo,
		Raw:          raw,
		SettlementID: raw["settlement-id"],
		Amount:       total,
		Currency:     raw["currency"],
		PeriodStart:  start,
		PeriodEnd:    end,
		DepositDate:  deposit,
	}, nil
}

func parseSettlementComponent(lineNo int, raw map[string]string) (Line, error) {
	if raw["settlement-id"] == "" {
		return Line{}, errors.New("settlement-id is empty")
	}
	amount, err := parsePlainAmount(raw["amount"])
	if err != nil {
		return Line{}, fmt.Errorf("amount: %w", err)
	}
	if _, err := checkTwoDecimals(amount, raw["amount"]); err != nil {
		return Line{}, fmt.Errorf("amount: %w", err)
	}
	posted, err := parseDottedDate(raw["posted-date"])
	if err != nil {
		return Line{}, fmt.Errorf("posted-date: %w", err)
	}
	return Line{
		Source:          Settlement,
		Kind:            KindComponent,
		LineNumber:      lineNo,
		Raw:             raw,
		SettlementID:    raw["settlement-id"],
		TxnType:         raw["transaction-type"],
		AmountType:      raw["amount-type"],
		Description:     raw["amount-description"],
		TxnRef:          raw["order-id"],
		SKU:             raw["sku"],
		Amount:          amount,
		PostedDate:      posted,
		PostedDateBasis: BasisPostedDate,
	}, nil
}
