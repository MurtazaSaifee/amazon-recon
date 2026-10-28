// Package configcsv reads the mapping config CSVs. It knows the file shape and the line each row came
// from; it knows nothing about the database. The store seeds its tables from these rows.
package configcsv

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"amazonrecon/internal/textfile"
)

// Record is one data row of a config CSV, with the CSV line it came from. The header is line 1.
type Record struct {
	Line   int
	Fields []string
}

// Read reads a config CSV. The header must match exactly, and every row must have as many fields as
// the header. A leading byte order mark is skipped. Each row keeps the line number it came from.
func Read(path string, header []string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	br := bufio.NewReader(f)
	if err := textfile.SkipBOM(br); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	cr := csv.NewReader(br)
	cr.FieldsPerRecord = len(header)
	cr.TrimLeadingSpace = false

	got, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("%s: header: %w", path, err)
	}
	if !slices.Equal(got, header) {
		return nil, fmt.Errorf("%s: header does not match the expected config shape", path)
	}

	var out []Record
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		line, _ := cr.FieldPos(0)
		out = append(out, Record{Line: line, Fields: rec})
	}
	return out, nil
}
