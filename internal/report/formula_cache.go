package report

import (
	"archive/zip"
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
func numericFormulaCache(path string) error {
	src, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer src.Close()

	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(out)
	for _, entry := range src.File {
		body, err := readZipEntry(entry)
		if err != nil {
			zw.Close()
			out.Close()
			os.Remove(tmp)
			return err
		}
		if strings.HasPrefix(entry.Name, "xl/worksheets/sheet") {
			body = textFormulaCell.ReplaceAll(body, []byte("<c $1$2><f"))
		}
		hdr := &zip.FileHeader{Name: entry.Name, Method: zip.Deflate, Modified: entry.Modified}
		w, err := zw.CreateHeader(hdr)
		if err == nil {
			_, err = w.Write(body)
		}
		if err != nil {
			zw.Close()
			out.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := zw.Close(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func readZipEntry(entry *zip.File) ([]byte, error) {
	rc, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
