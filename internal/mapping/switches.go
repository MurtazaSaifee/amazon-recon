package mapping

import "fmt"

// Switch names one interpretation of the matching logic that can be turned
// on or off. Later tickets add names to knownSwitches; callers do not change.
type Switch string

// knownSwitches is the registry of named switches. It is empty for now.
var knownSwitches = map[Switch]string{}

// SwitchSet is the set of enabled interpretation switches.
type SwitchSet map[Switch]bool

// NewSwitchSet builds a SwitchSet from names. It fails on a name that is not
// in the registry, so a typo cannot silently turn nothing on.
func NewSwitchSet(names ...Switch) (SwitchSet, error) {
	set := SwitchSet{}
	for _, name := range names {
		if _, ok := knownSwitches[name]; !ok {
			return nil, fmt.Errorf("unknown interpretation switch %q", name)
		}
		set[name] = true
	}
	return set, nil
}

// Enabled reports whether the named switch is on. A nil SwitchSet has none on.
func (s SwitchSet) Enabled(name Switch) bool {
	return s[name]
}
