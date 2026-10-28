package mapping

import (
	"fmt"
	"sort"
)

// Switch names one interpretation of the matching logic that can be turned
// on or off. Later tickets add names to knownSwitches; callers do not change.
type Switch string

// SwitchInfo describes one registered switch. Default is whether the switch is
// on in a fixed (non-baseline) run; a baseline run turns every switch off.
type SwitchInfo struct {
	Description string
	Default     bool
}

// knownSwitches is the registry of named switches. It is empty for now.
var knownSwitches = map[Switch]SwitchInfo{}

// SwitchesFor returns the switches a run uses. A baseline run has none. A fixed
// run has every registered switch whose default is on.
func SwitchesFor(baseline bool) SwitchSet {
	set := SwitchSet{}
	if baseline {
		return set
	}
	for name, info := range knownSwitches {
		if info.Default {
			set[name] = true
		}
	}
	return set
}

// Describe returns the description of a registered switch, or "" when it is not registered.
func Describe(name Switch) string {
	return knownSwitches[name].Description
}

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

// Names returns the enabled switch names in sorted order, so a recorded set is stable.
func (s SwitchSet) Names() []string {
	names := make([]string, 0, len(s))
	for name, on := range s {
		if on {
			names = append(names, string(name))
		}
	}
	sort.Strings(names)
	return names
}
