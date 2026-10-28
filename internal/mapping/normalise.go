// Package mapping turns one ingested Transaction line or Component into a
// record_ref, a set of Summary Contributions and warnings, using the
// payments and settlement mapping rules. It is pure: no database, no file IO.
package mapping

import (
	"regexp"
	"strings"
)

var whitespaceRun = regexp.MustCompile(`\s+`)

// Normalise is the single matching rule applied to config keys and source
// values alike: trim, upper-case, and turn each run of whitespace into one
// underscore. Nothing else (no dash folding, no punctuation stripping, no
// fuzzy matching) is applied.
func Normalise(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return whitespaceRun.ReplaceAllString(s, "_")
}
