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
