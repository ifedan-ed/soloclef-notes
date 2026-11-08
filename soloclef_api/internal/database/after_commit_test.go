package database

import "testing"

// The rule the whole write seam rests on: an effect registered inside a
// transaction fires once, after the commit, in the order it was registered —
// and never when nothing was committed. The transaction half of that is proved
// against a real database by the comparison in custom/docs/go-api-tier.md; this
// is the collection itself.
func TestEffectsFireInOrderOnlyWhenRun(t *testing.T) {
	var fired []string
	effects := &Effects{}
	effects.After(func() { fired = append(fired, "first") })
	effects.After(func() { fired = append(fired, "second") })

	if len(fired) != 0 {
		t.Fatalf("an effect fired at registration: %v", fired)
	}
	effects.run()
	if len(fired) != 2 || fired[0] != "first" || fired[1] != "second" {
		t.Errorf("fired %v, want first then second", fired)
	}
}

// A write that returns an error never reaches run(), so an unrun Effects is
// what a rolled-back transaction leaves behind: nothing told anyone.
func TestAnUnrunEffectsTellsNobody(t *testing.T) {
	told := false
	effects := &Effects{}
	effects.After(func() { told = true })
	if told {
		t.Error("the effect fired without the commit")
	}
}
