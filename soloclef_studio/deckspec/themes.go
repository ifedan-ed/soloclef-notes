package deckspec

// Theme is one look a deck can be drawn in, as the model chooses it: an id
// and what it suits. The colours and fonts are the renderer's
// (deckrender's palettes); its tests hold the two lists to each other, so
// the model is never offered a look nothing draws.
type Theme struct {
	ID   string
	Suit string
}

// Themes is the catalogue. The first six are PedsHub Tools' palettes
// (deck-themes.json), the last three the looks Soloclef's video dialog
// offers (classic, whiteboard, playful); "dark" and "minimal" there are
// night and swiss here.
var Themes = []Theme{
	{"clinical", "calm blue on white; reads well on any projector; the safe choice for teaching and reports"},
	{"amber", "warm amber on white; friendlier, for students, parents and general audiences"},
	{"teal", "quiet teal on white; clinical and understated"},
	{"swiss", "almost monochrome slate with very large type; minimal, lets the content carry it"},
	{"signal", "black and strong red, heavy ink; for a bright room or a poor projector"},
	{"night", "dark navy slides with light text and serif headings; a lecture theatre or a dark style"},
	{"classic", "warm off-white paper, deep navy text, serif headings, one blue accent; editorial"},
	{"whiteboard", "pure white with marker-ink black, one red and one blue accent; a teacher at a board"},
	{"playful", "soft warm paper, rounded cards, coral and green accents; light and friendly"},
}

// themeIDs is the ids, for a sentence and the schema.
func themeIDs() []string {
	ids := make([]string, len(Themes))
	for i, theme := range Themes {
		ids[i] = theme.ID
	}
	return ids
}

// knownTheme reports whether id is in the catalogue.
func knownTheme(id string) bool {
	for _, theme := range Themes {
		if theme.ID == id {
			return true
		}
	}
	return false
}
