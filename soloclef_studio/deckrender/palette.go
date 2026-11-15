package deckrender

// palette is one theme's look: its colours (six hex digits), its type,
// and the few choices of shape that tell themes apart. Every slide kind is
// laid out the same way in every theme; a theme changes how it looks, not
// where things are, so a layout that fits fits in all of them.
//
// The first six are PedsHub Tools' themes (deck-themes.json: Clinical
// Blue, Teaching Amber, Case File, Swiss Grid, Signal, Grand Rounds
// Night); the last three are the looks Soloclef's video dialog names.
type palette struct {
	paper, ink, muted, rule string
	accent, accentAlt       string
	tint, tintAlt           string
	head, body              family
	// dark is a theme whose paper is dark: text is moved towards the
	// paper's opposite when it must be made legible.
	dark bool
	// cover is the title and section slides' ground: "paper", "accent" or
	// "tint".
	cover string
	// heading is how a slide's heading is set off: "rule" (a short accent
	// rule under it), "bar" (an accent bar beside it) or "band" (a filled
	// band behind it).
	heading string
	// radius is a card's corner, in points (0 is square).
	radius float64
	// outline draws cards as an ink outline on the paper instead of a
	// tinted fill (the whiteboard's marker look).
	outline bool
	// transition is how a deck's slides come in: "fade", "push" or "" (a
	// cut).
	transition string
}

var palettes = map[string]palette{
	"clinical": {
		paper: "FFFFFF", ink: "1F2937", muted: "4B5563", rule: "E5E7EB",
		accent: "2563EB", accentAlt: "D97706", tint: "EFF6FF", tintAlt: "FEF3C7",
		head: sans, body: sans, cover: "paper", heading: "rule", radius: 6,
	},
	"amber": {
		paper: "FFFFFF", ink: "1C1917", muted: "57534E", rule: "EDE9E4",
		accent: "B45309", accentAlt: "2563EB", tint: "FEF3C7", tintAlt: "EFF6FF",
		head: sans, body: sans, cover: "tint", heading: "bar", radius: 6,
	},
	"teal": {
		paper: "FFFFFF", ink: "134E4A", muted: "4B5563", rule: "E3EDEB",
		accent: "0F766E", accentAlt: "B45309", tint: "CCFBF1", tintAlt: "FEF3C7",
		head: sans, body: sans, cover: "paper", heading: "rule", radius: 4,
	},
	"swiss": {
		paper: "FFFFFF", ink: "0F172A", muted: "475569", rule: "E2E8F0",
		accent: "334155", accentAlt: "64748B", tint: "F1F5F9", tintAlt: "E2E8F0",
		head: sans, body: sans, cover: "paper", heading: "rule", radius: 0,
	},
	"signal": {
		paper: "FFFFFF", ink: "000000", muted: "27272A", rule: "A1A1AA",
		accent: "B91C1C", accentAlt: "111827", tint: "FEE2E2", tintAlt: "F3F4F6",
		head: sans, body: sans, cover: "accent", heading: "band", radius: 0,
	},
	"night": {
		paper: "0F1B33", ink: "F1F5F9", muted: "B8C4D8", rule: "33476E",
		accent: "7DB3FF", accentAlt: "F2B866", tint: "1A2A4C", tintAlt: "2B2A44",
		head: serif, body: sans, dark: true, cover: "paper", heading: "bar", radius: 6, transition: "fade",
	},
	"classic": {
		paper: "FBF8F2", ink: "14213D", muted: "4A5568", rule: "E6DFD3",
		accent: "2F5DA8", accentAlt: "A0522D", tint: "EEF2FA", tintAlt: "F6EBDD",
		head: serif, body: sans, cover: "paper", heading: "rule", radius: 4, transition: "fade",
	},
	"whiteboard": {
		paper: "FFFFFF", ink: "1A1A1A", muted: "404040", rule: "D4D4D4",
		accent: "1D4ED8", accentAlt: "C81E1E", tint: "FFFFFF", tintAlt: "FFFFFF",
		head: sans, body: sans, cover: "paper", heading: "rule", radius: 10, outline: true,
	},
	"playful": {
		paper: "FFFBF6", ink: "2E2A3B", muted: "5F586C", rule: "E9E1D6",
		accent: "D9503E", accentAlt: "15806F", tint: "FFE3D8", tintAlt: "D5F2E7",
		head: sans, body: sans, cover: "tint", heading: "rule", radius: 16, transition: "push",
	},
}

// toward is the colour text is moved towards when it must read better on
// this theme's paper: its ink.
func (p palette) toward() string { return p.ink }
