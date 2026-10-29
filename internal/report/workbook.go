// Package report writes the reconciliation workbook. It has no database access: it lays out the
// numbers that the recon package computed, one sheet per file of this package.
package report

import (
	"fmt"
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
		return fmt.Errorf("create the workbook directory: %w", err)
	}
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", SummarySheet); err != nil {
		return fmt.Errorf("name the Summary sheet: %w", err)
	}
	styles, err := newStyles(f)
	if err != nil {
		return fmt.Errorf("create the cell styles: %w", err)
	}
	if err := writeSummary(f, r, styles); err != nil {
		return fmt.Errorf("write the Summary sheet: %w", err)
	}
	if err := writeConsolidated(f, r, styles.bold, styles.money); err != nil {
		return fmt.Errorf("write the Consolidated Data sheet: %w", err)
	}
	if _, err := f.NewSheet(AuditTrailSheet); err != nil {
		return fmt.Errorf("add the Audit Trail sheet: %w", err)
	}
	if err := writeAuditTrail(f, r.Audit, styles.bold, styles.money); err != nil {
		return fmt.Errorf("write the Audit Trail sheet: %w", err)
	}
	if err := writeOutOfScope(f, r, styles.bold, styles.money); err != nil {
		return fmt.Errorf("write the Out of Scope sheet: %w", err)
	}
	// Excel recalculates the formulas when it opens the file; the stored values serve every other reader.
	fullCalc := true
	if err := f.SetCalcProps(&excelize.CalcPropsOptions{FullCalcOnLoad: &fullCalc}); err != nil {
		return fmt.Errorf("set the calculation properties: %w", err)
	}
	if err := f.SaveAs(path); err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	return numericFormulaCache(path)
}

// styles are the cell styles every sheet shares: bold headers and the #,##0.00 money format.
type styles struct {
	bold  int
	money int
	// indent sets a Summary line label apart from its group: left aligned, indented as in the sample.
	indent int
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
	indent, err := f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{Horizontal: "left", Indent: 4}})
	if err != nil {
		return styles{}, err
	}
	return styles{bold: bold, money: money, indent: indent}, nil
}
