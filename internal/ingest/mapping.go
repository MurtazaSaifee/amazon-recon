package ingest

import (
	"fmt"

	"github.com/shopspring/decimal"

	"amazonrecon/internal/mapping"
	"amazonrecon/internal/source"
	"amazonrecon/internal/store"
	"amazonrecon/internal/timefmt"
)

// mappedRow scopes a line and maps it. Every Transaction line and Component is mapped, in or
// out of scope, so its record_ref and matched rules are stored. Only in-scope lines produce
// Contributions; that is decided when the rows are written.
func mappedRow(m *mapping.Mapper, fileName string, l source.Line, headerID string) (store.SourceRow, error) {
	row := scopedRow(fileName, l, headerID)
	var res mapping.Result
	switch l.Source {
	case source.Payments:
		tl, err := transactionLine(l)
		if err != nil {
			return store.SourceRow{}, err
		}
		res = m.MapTransaction(tl)
	case source.Settlement:
		res = m.MapComponent(component(l))
	default:
		return store.SourceRow{}, fmt.Errorf("line %d: unknown source %q", l.LineNumber, l.Source)
	}
	row.Mapping = &res
	return row, nil
}

// transactionLine converts a stored Payments line into the mapping input. Each typed amount
// column is read from the raw payload and keyed by its Amount field name. The total is read
// from the line's own amount.
func transactionLine(l source.Line) (mapping.TransactionLine, error) {
	amounts := make(map[string]decimal.Decimal, len(source.PaymentsAmountColumns))
	for _, col := range source.PaymentsAmountColumns {
		d, err := source.ParseAmount(l.Raw[col])
		if err != nil {
			return mapping.TransactionLine{}, fmt.Errorf("line %d, %s: %w", l.LineNumber, col, err)
		}
		amounts[mapping.AmountFieldForColumn(col)] = d
	}
	return mapping.TransactionLine{
		Keys:            keysOf(l),
		TransactionType: l.TxnType,
		Amounts:         amounts,
		Total:           l.Amount,
	}, nil
}

// component converts a stored Settlement component into the mapping input.
func component(l source.Line) mapping.Component {
	return mapping.Component{
		Keys:              keysOf(l),
		TransactionType:   l.TxnType,
		AmountType:        l.AmountType,
		AmountDescription: l.Description,
		Amount:            l.Amount,
	}
}

// keysOf gives the common key fields a record_ref template can name. The date is the posted
// date, in UTC. Shipment and merchant order IDs exist on Components only, so they are empty
// for Transaction lines.
func keysOf(l source.Line) mapping.Keys {
	return mapping.Keys{
		TxnRef:          l.TxnRef,
		SKU:             l.SKU,
		Date:            timefmt.Date(l.PostedDate),
		SettlementID:    l.SettlementID,
		Description:     l.Description,
		ShipmentID:      l.Raw["shipment-id"],
		MerchantOrderID: l.Raw["merchant-order-id"],
	}
}
