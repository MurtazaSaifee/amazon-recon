// Package report writes the reconciliation workbook. It has no database access: it lays out the
// numbers that the recon package computed, one sheet per file of this package.
package report

import (
	"os"
	"path/filepath"

	"github.com/xuri/excelize/v2"

	"amazonrecon/internal/recon"
)

// WriteWorkbook writes the reconciliation workbook to path, creating its directory. The sheets are
// Summary (with the run header and the tie-out), Consolidated Data, Audit Trail and Out of Scope.
// Amounts are written as decimal text, so no value passes through float64.
func WriteWorkbook(path string, r recon.Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", SummarySheet); err != nil {
		return err
	}
	styles, err := newStyles(f)
	if err != nil {
		return err
	}
	if err := writeSummary(f, r, styles.bold, styles.money); err != nil {
		return err
	}
	if err := writeConsolidated(f, r, styles.bold, styles.money); err != nil {
		return err
	}
	if _, err := f.NewSheet(AuditTrailSheet); err != nil {
		return err
	}
	if err := writeAuditTrail(f, r.Audit, styles.bold, styles.money); err != nil {
		return err
	}
	if err := writeOutOfScope(f, r, styles.bold, styles.money); err != nil {
		return err
	}
	// Excel recalculates the formulas when it opens the file; the stored values serve every other reader.
	fullCalc := true
	if err := f.SetCalcProps(&excelize.CalcPropsOptions{FullCalcOnLoad: &fullCalc}); err != nil {
		return err
	}
	if err := f.SaveAs(path); err != nil {
		return err
	}
	return numericFormulaCache(path)
}

// styles are the cell styles every sheet shares: bold headers and the #,##0.00 money format.
type styles struct {
	bold  int
	money int
}

func newStyles(f *excelize.File) (styles, error) {
	bold, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return styles{}, err
	}
	money, err := f.NewStyle(&excelize.Style{NumFmt: 4}) // #,##0.00
	if err != nil {
		return styles{}, err
	}
	return styles{bold: bold, money: money}, nil
}
