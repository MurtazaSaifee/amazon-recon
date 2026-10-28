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
		res = m.MapTransaction(transactionLine(l))
	case source.Settlement:
		res = m.MapComponent(component(l))
	default:
		return store.SourceRow{}, fmt.Errorf("line %d: unknown source %q", l.LineNumber, l.Source)
	}
	row.Mapping = &res
	return row, nil
}

// transactionLine converts a parsed Payments line into the mapping input. The typed amounts were
// parsed once by the reader; each is keyed by its Amount field name here.
func transactionLine(l source.Line) mapping.TransactionLine {
	amounts := make(map[string]decimal.Decimal, len(l.Amounts))
	for col, d := range l.Amounts {
		amounts[mapping.AmountFieldForColumn(col)] = d
	}
	return mapping.TransactionLine{
		Keys:            keysOf(l),
		TransactionType: l.TxnType,
		Amounts:         amounts,
		Total:           l.Amount,
	}
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
