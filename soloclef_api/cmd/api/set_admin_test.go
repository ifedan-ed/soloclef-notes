package main

import "testing"

// The flag is exactly true or false, as every switch of this tier is: a
// typo must not quietly make, or unmake, an administrator.
func TestTheAdministratorFlagIsExactlyTrueOrFalse(t *testing.T) {
	for text, want := range map[string]bool{"true": true, "false": false} {
		if got, err := administratorFlag(text); err != nil || got != want {
			t.Errorf("%q read as %v, %v", text, got, err)
		}
	}
	for _, text := range []string{"", "yes", "1", "TRUE", "on", " true"} {
		if _, err := administratorFlag(text); err == nil {
			t.Errorf("%q was read as a flag", text)
		}
	}
}
