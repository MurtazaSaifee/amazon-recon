package mapping

import "testing"

func TestSwitchSet(t *testing.T) {
	t.Run("no names gives an empty set", func(t *testing.T) {
		set, err := NewSwitchSet()
		if err != nil {
			t.Fatalf("NewSwitchSet() error = %v", err)
		}
		if set.Enabled("anything") {
			t.Fatalf("empty set reports a switch as enabled")
		}
	})

	t.Run("an unregistered name is rejected, not silently ignored", func(t *testing.T) {
		if _, err := NewSwitchSet("no-such-switch"); err == nil {
			t.Fatalf("NewSwitchSet with an unregistered name returned no error")
		}
	})

	t.Run("nil set has no switches enabled", func(t *testing.T) {
		var set SwitchSet
		if set.Enabled("anything") {
			t.Fatalf("nil set reports a switch as enabled")
		}
	})
}

// withRegistry replaces the switch registry for one test and restores it afterwards.
func withRegistry(t *testing.T, reg map[Switch]SwitchInfo) {
	t.Helper()
	saved := knownSwitches
	knownSwitches = reg
	t.Cleanup(func() { knownSwitches = saved })
}

func TestSwitchesForMode(t *testing.T) {
	withRegistry(t, map[Switch]SwitchInfo{
		"b-fix": {Description: "second fix", Default: true},
		"a-fix": {Description: "first fix", Default: true},
		"off":   {Description: "off by default", Default: false},
	})

	t.Run("baseline turns every switch off", func(t *testing.T) {
		set := SwitchesFor(true)
		if names := set.Names(); len(names) != 0 {
			t.Fatalf("baseline enabled %v, want none", names)
		}
	})

	t.Run("fixed mode enables every registry switch whose default is on", func(t *testing.T) {
		set := SwitchesFor(false)
		if got, want := set.Names(), []string{"a-fix", "b-fix"}; !equalStrings(got, want) {
			t.Fatalf("fixed mode enabled %v, want %v (sorted)", got, want)
		}
		if set.Enabled("off") {
			t.Fatalf("switch with default off was enabled in fixed mode")
		}
	})

	t.Run("names are sorted so the recorded set is stable", func(t *testing.T) {
		set, err := NewSwitchSet("b-fix", "a-fix")
		if err != nil {
			t.Fatalf("NewSwitchSet: %v", err)
		}
		if got, want := set.Names(), []string{"a-fix", "b-fix"}; !equalStrings(got, want) {
			t.Fatalf("Names() = %v, want %v", got, want)
		}
	})

}
