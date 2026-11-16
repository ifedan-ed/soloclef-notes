package studioviews

import "testing"

// The contents list drops a heading's own numbering, a zero-padded one
// included, and keeps a heading that starts with a year.
func TestHeadingLabelDropsNumbering(t *testing.T) {
	for raw, want := range map[string]string{
		"1. Introduction":      "Introduction",
		"01 Executive summary": "Executive summary",
		"IV. Results":          "Results",
		"2024 in review":       "2024 in review",
		"10":                   "10",
	} {
		if got := headingLabel(raw); got != want {
			t.Errorf("headingLabel(%q) = %q, want %q", raw, got, want)
		}
	}
}
