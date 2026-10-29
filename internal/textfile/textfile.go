// Package textfile holds the line-reading helpers shared by the report readers and the config readers.
package textfile

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
)

// SkipBOM drops a leading UTF-8 byte order mark, if there is one. Every input file the pipeline reads may
// start with one, so every reader calls this first.
func SkipBOM(br *bufio.Reader) error {
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

// ReadLine returns the next line without its line ending. ok is false at the end of input. A final
// line without a newline is still returned.
func ReadLine(br *bufio.Reader) (text string, ok bool, err error) {
	s, err := br.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, err
	}
	if s == "" {
		return "", false, nil
	}
	return strings.TrimRight(s, "\r\n"), true, nil
}
