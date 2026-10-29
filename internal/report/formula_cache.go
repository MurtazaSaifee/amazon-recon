package report

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// textFormulaCell matches a formula cell that excelize wrote with a text type. excelize stamps
// t="str" on every formula, so its cached value is text. Every formula in this workbook yields a
// number, so the text type is wrong and is dropped.
var textFormulaCell = regexp.MustCompile(`<c ([^>]*?) t="str"([^>]*)><f`)

// numericFormulaCache rewrites a saved workbook so that its formula cells cache a number. Excel
// recalculates formulas on open; previewers and readers that do not recalculate show the cached value.
// The rewrite goes to a temporary file that replaces the workbook only when it is complete.
func numericFormulaCache(path string) (err error) {
	src, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open workbook for the formula cache: %w", err)
	}
	defer src.Close()

	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	// The temporary file is closed exactly once, on every path, and removed unless it replaced the workbook.
	closed := false
	closeOut := func() error {
		if closed {
			return nil
		}
		closed = true
		return out.Close()
	}
	defer func() {
		if cerr := closeOut(); err == nil && cerr != nil {
			err = fmt.Errorf("close %s: %w", tmp, cerr)
		}
		if err != nil {
			os.Remove(tmp)
		}
	}()

	zw := zip.NewWriter(out)
	for _, entry := range src.File {
		body, err := readZipEntry(entry)
		if err != nil {
			return fmt.Errorf("read %s in the workbook: %w", entry.Name, err)
		}
		if strings.HasPrefix(entry.Name, "xl/worksheets/sheet") {
			body = textFormulaCell.ReplaceAll(body, []byte("<c $1$2><f"))
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: entry.Name, Method: zip.Deflate, Modified: entry.Modified})
		if err != nil {
			return fmt.Errorf("write %s: %w", entry.Name, err)
		}
		if _, err := w.Write(body); err != nil {
			return fmt.Errorf("write %s: %w", entry.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finish %s: %w", tmp, err)
	}
	if err := closeOut(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func readZipEntry(entry *zip.File) ([]byte, error) {
	rc, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
