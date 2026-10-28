package pipelinetest

import (
	"archive/zip"
	"context"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"amazonrecon/internal/recon"
	"amazonrecon/internal/report"
)

// formulaCell matches a formula cell in a worksheet part and captures its opening tag.
var formulaCell = regexp.MustCompile(`<c [^>]*>\s*<f>`)

// A formula cell caches the number its formula gives. Excel recalculates it on open, but a previewer
// that does not recalculate shows the cached value, so the cache must be numeric, not text.
func TestFormulaCellsCacheNumericValues(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res := ingestFixtures(t, pool)
	rep, err := recon.Build(ctx, pool, res.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	path := filepath.Join(t.TempDir(), "formulas.xlsx")
	if err := report.WriteWorkbook(path, rep); err != nil {
		t.Fatalf("write workbook: %v", err)
	}

	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open workbook zip: %v", err)
	}
	defer zr.Close()
	formulas := 0
	for _, entry := range zr.File {
		if !strings.HasPrefix(entry.Name, "xl/worksheets/sheet") {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			t.Fatalf("open %s: %v", entry.Name, err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name, err)
		}
		for _, tag := range formulaCell.FindAllString(string(body), -1) {
			formulas++
			if strings.Contains(tag, ` t="str"`) {
				t.Errorf("%s: formula cell %s caches a text value, want a number", entry.Name, tag)
			}
		}
	}
	if formulas == 0 {
		t.Fatal("the workbook has no formula cells to check")
	}
}
