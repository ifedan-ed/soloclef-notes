package deliverables

import (
	"encoding/xml"
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"
)

// A deck read back as it will be drawn: where every shape's text, line,
// picture and fill sits on each slide, in points, and what size and colour
// each run of text is set in, with what it sits on. The placement rules
// (layout_rules.go) hold these against the words LibreOffice actually drew.
//
// Why from the deck's own XML and not from a picture of it: a model's
// program writes the deck through python-pptx, so every box, size and colour
// is written down exactly; reading them back is exact, repeatable and costs
// microseconds, where a model looking at a picture is none of the three.
// This is the way PedsHub Tools holds its own themes (tools-go
// internal/pptxcheck), extended to decks whose code nobody here wrote: so
// placeholders inherit from their layout and master, colours come from the
// theme, and shapes can sit inside groups.

// emuPerPoint converts the deck's English Metric Units to points.
const emuPerPoint = 12700

// rect is a box in points from the slide's top left.
type rect struct{ x, y, w, h float64 }

func (r rect) right() float64  { return r.x + r.w }
func (r rect) bottom() float64 { return r.y + r.h }

// covers reports whether a point lies inside the box shrunk by inset on
// every side.
func (r rect) covers(x, y, inset float64) bool {
	return x > r.x+inset && x < r.right()-inset && y > r.y+inset && y < r.bottom()-inset
}

// point is a place on a slide, in points.
type point struct{ x, y float64 }

// textRun is one run of text as it will be drawn.
type textRun struct {
	text string
	// size is in points, after any shrink the frame's autofit states.
	size float64
	bold bool
	// colour and ground are six hex digits; "" is not known (a gradient,
	// a picture or a chart under the text), and then no contrast is judged.
	colour, ground string
}

// textFrame is one shape's or table's text on a slide.
type textFrame struct {
	name string
	box  rect
	text string
	// paragraphs is how many paragraphs carry text.
	paragraphs int
	// wraps: the text wraps inside the box (not wrap="none"); grows: the
	// box grows to fit its text (spAutoFit), as LibreOffice draws it.
	wraps, grows bool
	// rotated text is not held to its box: its box is not where it is drawn.
	rotated bool
	table   bool
	z       int
	runs    []textRun
}

// cover is something drawn that hides what is under it: a filled shape or
// a picture.
type cover struct {
	name    string
	box     rect
	z       int
	picture bool
}

// stroke is a visible line: a connector, a line shape or an unfilled
// freeform, as the segments it is drawn along.
type stroke struct {
	name     string
	segments [][2]point
}

// slideShapes is one slide read back.
type slideShapes struct {
	number   int
	frames   []textFrame
	covers   []cover
	strokes  []stroke
	pictures []cover
}

// deckShapes is a deck read back: its slide size in points and each slide.
type deckShapes struct {
	width, height float64
	slides        []slideShapes
}

// theme is what a slide's colours are resolved through: the master's
// colour map (bg1 to lt1 and so on) and the theme's scheme.
type theme struct {
	scheme map[string]string
	colors map[string]string
	// backgrounds are the theme's background fill styles, by bgRef index.
	backgrounds []*element
}

// colour resolves a colour element (srgbClr, schemeClr, sysClr, prstClr)
// with its modifiers to six hex digits and an opacity from 0 to 1. ok is
// false for a colour that cannot be known here.
func (t theme) colour(e *element, placeholder string) (hex string, alpha float64, ok bool) {
	if e == nil {
		return "", 0, false
	}
	switch e.name {
	case "srgbClr":
		hex, _ = e.attr("", "val")
	case "sysClr":
		hex, _ = e.attr("", "lastClr")
	case "prstClr":
		name, _ := e.attr("", "val")
		hex = map[string]string{"black": "000000", "white": "FFFFFF"}[name]
	case "schemeClr":
		name, _ := e.attr("", "val")
		if name == "phClr" {
			hex = placeholder
			break
		}
		if mapped, found := t.colors[name]; found {
			name = mapped
		}
		hex = t.scheme[name]
	default:
		return "", 0, false
	}
	hex = strings.ToUpper(hex)
	if _, err := strconv.ParseUint(hex, 16, 32); err != nil || len(hex) != 6 {
		return "", 0, false
	}
	alpha = 1
	for _, modifier := range e.children {
		value, err := strconv.ParseFloat(attrOr(modifier, "val", ""), 64)
		if err != nil {
			continue
		}
		value /= 100_000
		switch modifier.name {
		case "alpha":
			alpha = value
		case "lumMod", "lumOff":
			// lumMod and lumOff come as a pair (a theme colour made
			// lighter or darker); each is applied to HSL lightness.
			hex = adjustLightness(hex, modifier.name, value)
		case "shade":
			hex = blend(hex, "000000", 1-value)
		case "tint":
			hex = blend(hex, "FFFFFF", 1-value)
		}
	}
	return hex, alpha, true
}

// fillOf is the colour element of a fill, and whether the fill is one whose
// colour is known: a solid fill. A gradient, picture or pattern fill is
// drawn, but what colour text on it sits on is not known.
func fillOf(parent *element) (state fillState, colour *element) {
	if parent == nil {
		return fillUnset, nil
	}
	for _, c := range parent.children {
		if c.space != drawingNS {
			continue
		}
		switch c.name {
		case "noFill":
			return fillNone, nil
		case "solidFill":
			if len(c.children) > 0 {
				return fillSolid, c.children[0]
			}
			return fillUnknown, nil
		case "gradFill", "blipFill", "pattFill", "grpFill":
			return fillUnknown, nil
		}
	}
	return fillUnset, nil
}

type fillState int

const (
	fillUnset fillState = iota
	fillNone
	fillSolid
	fillUnknown
)

// attrOr is an unprefixed attribute, or otherwise.
func attrOr(e *element, name, otherwise string) string {
	if e == nil {
		return otherwise
	}
	if value, found := e.attr("", name); found {
		return value
	}
	return otherwise
}

// part is one XML part of the deck with its relationships, parsed once.
type part struct {
	root  *element
	links map[string]relationship
	name  string
}

// deckReader reads a deck's parts, each once.
type deckReader struct {
	archive *office
	parts   map[string]*part
}

func (d *deckReader) read(name string) (*part, error) {
	if p, found := d.parts[name]; found {
		return p, nil
	}
	raw, err := d.archive.read(name, maxXMLPartBytes)
	if err != nil {
		return nil, err
	}
	root, err := parseXML(raw)
	if err != nil {
		return nil, &errOffice{"PPTX part " + name + " is not valid XML"}
	}
	links, err := d.archive.relationships(path.Dir(name)+"/_rels/"+path.Base(name)+".rels", name)
	if err != nil {
		return nil, err
	}
	p := &part{root: root, links: links, name: name}
	d.parts[name] = p
	return p, nil
}

// linked is the part a part links to by relationship type, or nil.
func (d *deckReader) linked(from *part, kind string) (*part, error) {
	for _, link := range from.links {
		if !link.external && strings.HasSuffix(link.kind, "/"+kind) && d.archive.parts[link.target] != nil {
			return d.read(link.target)
		}
	}
	return nil, nil
}

// readDeckShapes reads every slide of a deck back. The deck has passed
// checkPPTX, so its package and slide list are sound.
func readDeckShapes(data []byte) (deckShapes, error) {
	archive, err := openOffice(data, "PPTX")
	if err != nil {
		return deckShapes{}, err
	}
	reader := &deckReader{archive: archive, parts: map[string]*part{}}
	presentation, err := reader.read("ppt/presentation.xml")
	if err != nil {
		return deckShapes{}, err
	}
	var deck deckShapes
	size := presentation.root.child(slidesNS, "sldSz")
	cx, _ := strconv.ParseFloat(attrOr(size, "cx", "0"), 64)
	cy, _ := strconv.ParseFloat(attrOr(size, "cy", "0"), 64)
	deck.width, deck.height = cx/emuPerPoint, cy/emuPerPoint
	defaults := presentation.root.child(slidesNS, "defaultTextStyle")
	list := presentation.root.child(slidesNS, "sldIdLst")
	if list == nil {
		return deck, nil
	}
	number := 0
	for _, entry := range list.children {
		if entry.name != "sldId" || entry.space != slidesNS {
			continue
		}
		number++
		id, _ := entry.attr(officeRel, "id")
		link, found := presentation.links[id]
		if !found || archive.parts[link.target] == nil {
			continue
		}
		slide, err := readSlide(reader, link.target, number, defaults)
		if err != nil {
			return deckShapes{}, err
		}
		slide.number = number
		deck.slides = append(deck.slides, slide)
	}
	return deck, nil
}

// slideContext is what one slide's shapes are resolved against.
type slideContext struct {
	theme           theme
	layout, master  *element
	defaults        *element
	background      string
	backgroundKnown bool
	shapes          *slideShapes
	z               int
	layers          []layer
}

// layer is a drawn area text may sit on, in drawing order.
type layer struct {
	box   rect
	state fillState
	hex   string
	alpha float64
}

func readSlide(reader *deckReader, name string, number int, defaults *element) (slideShapes, error) {
	slide, err := reader.read(name)
	if err != nil {
		return slideShapes{}, err
	}
	layout, err := reader.linked(slide, "slideLayout")
	if err != nil {
		return slideShapes{}, err
	}
	var master, themePart *part
	if layout != nil {
		if master, err = reader.linked(layout, "slideMaster"); err != nil {
			return slideShapes{}, err
		}
	}
	if master != nil {
		if themePart, err = reader.linked(master, "theme"); err != nil {
			return slideShapes{}, err
		}
	}
	ctx := &slideContext{defaults: defaults, shapes: &slideShapes{number: number}}
	ctx.theme = readTheme(themePart, master, slide)
	if layout != nil {
		ctx.layout = layout.root
	}
	if master != nil {
		ctx.master = master.root
	}
	ctx.background, ctx.backgroundKnown = ctx.slideBackground(slide.root)
	tree := slide.root.child(slidesNS, "cSld").child(slidesNS, "spTree")
	if tree != nil {
		ctx.walk(tree, identity)
	}
	return *ctx.shapes, nil
}

// readTheme reads the theme's colour scheme and the colour map the slide
// uses: the master's, unless the slide overrides it.
func readTheme(themePart, master, slide *part) theme {
	t := theme{scheme: map[string]string{}, colors: map[string]string{}}
	if themePart != nil {
		elements := themePart.root.child(drawingNS, "themeElements")
		if scheme := elements.child(drawingNS, "clrScheme"); scheme != nil {
			for _, entry := range scheme.children {
				if len(entry.children) == 0 {
					continue
				}
				if hex, _, ok := t.colour(entry.children[0], ""); ok {
					t.scheme[entry.name] = hex
				}
			}
		}
		if list := elements.child(drawingNS, "fmtScheme").child(drawingNS, "bgFillStyleLst"); list != nil {
			t.backgrounds = list.children
		}
	}
	mapping := (*element)(nil)
	if master != nil {
		mapping = master.root.child(slidesNS, "clrMap")
	}
	if override := slide.root.child(slidesNS, "clrMapOvr").child(drawingNS, "overrideClrMapping"); override != nil {
		mapping = override
	}
	if mapping != nil {
		for _, a := range mapping.attrs {
			t.colors[a.Name.Local] = a.Value
		}
	} else {
		// The colour map every Office template writes.
		for k, v := range map[string]string{"bg1": "lt1", "tx1": "dk1", "bg2": "lt2", "tx2": "dk2"} {
			t.colors[k] = v
		}
	}
	return t
}

// slideBackground is the slide's background colour: its own, else its
// layout's, else its master's, else the colour map's bg1. known is false
// for a gradient or picture background.
func (c *slideContext) slideBackground(slide *element) (string, bool) {
	for _, owner := range []*element{slide, c.layout, c.master} {
		bg := owner.child(slidesNS, "cSld").child(slidesNS, "bg")
		if bg == nil {
			continue
		}
		if properties := bg.child(slidesNS, "bgPr"); properties != nil {
			state, colour := fillOf(properties)
			switch state {
			case fillSolid:
				hex, _, ok := c.theme.colour(colour, "")
				return hex, ok
			case fillUnknown:
				return "", false
			}
			continue
		}
		if ref := bg.child(slidesNS, "bgRef"); ref != nil {
			index, _ := strconv.Atoi(attrOr(ref, "idx", "0"))
			placeholder := ""
			if len(ref.children) > 0 {
				placeholder, _, _ = c.theme.colour(ref.children[0], "")
			}
			// 1001 and on name the theme's background fill styles; only a
			// solid one is a known colour.
			if index > 1000 && index-1001 < len(c.theme.backgrounds) {
				style := c.theme.backgrounds[index-1001]
				if style.name == "solidFill" && len(style.children) > 0 {
					hex, _, ok := c.theme.colour(style.children[0], placeholder)
					return hex, ok
				}
				return "", false
			}
			return placeholder, placeholder != ""
		}
	}
	hex, _, ok := c.theme.colour(&element{name: "schemeClr", attrs: attrList("val", "bg1")}, "")
	return hex, ok
}

// transform maps a shape's own coordinates (EMU, inside any groups) to
// slide points.
type transform struct{ sx, sy, dx, dy float64 }

var identity = transform{sx: 1.0 / emuPerPoint, sy: 1.0 / emuPerPoint}

func (t transform) apply(x, y float64) point { return point{x*t.sx + t.dx, y*t.sy + t.dy} }

// geometry is a transform element read: its box in the shape's own space,
// rotation and flips.
type geometry struct {
	x, y, w, h   float64
	rotated      bool
	flipH, flipV bool
	ok           bool
}

func readGeometry(xfrm *element) geometry {
	if xfrm == nil {
		return geometry{}
	}
	off, ext := xfrm.child(drawingNS, "off"), xfrm.child(drawingNS, "ext")
	if off == nil || ext == nil {
		return geometry{}
	}
	number := func(e *element, name string) float64 {
		v, _ := strconv.ParseFloat(attrOr(e, name, "0"), 64)
		return v
	}
	rotation, _ := strconv.Atoi(attrOr(xfrm, "rot", "0"))
	return geometry{
		x: number(off, "x"), y: number(off, "y"), w: number(ext, "cx"), h: number(ext, "cy"),
		// Turned by a half turn the box is where it was; any other turn
		// moves the text off its stated box.
		rotated: rotation%10_800_000 != 0,
		flipH:   attrOr(xfrm, "flipH", "0") == "1", flipV: attrOr(xfrm, "flipV", "0") == "1",
		ok: true,
	}
}

// box is the geometry on the slide.
func (g geometry) box(t transform) rect {
	a, b := t.apply(g.x, g.y), t.apply(g.x+g.w, g.y+g.h)
	return rect{math.Min(a.x, b.x), math.Min(a.y, b.y), math.Abs(b.x - a.x), math.Abs(b.y - a.y)}
}

// walk reads a shape tree in drawing order.
func (c *slideContext) walk(tree *element, t transform) {
	for _, shape := range tree.children {
		if shape.space != slidesNS {
			continue
		}
		switch shape.name {
		case "sp":
			c.shape(shape, t)
		case "cxnSp":
			c.z++
			properties := shape.child(slidesNS, "spPr")
			g := readGeometry(properties.child(drawingNS, "xfrm"))
			if g.ok && c.lineVisible(properties, shape.child(slidesNS, "style")) {
				prst := attrOr(properties.child(drawingNS, "prstGeom"), "prst", "line")
				c.shapes.strokes = append(c.shapes.strokes, stroke{name: shapeName(shape), segments: presetSegments(prst, g, t)})
			}
		case "pic":
			c.z++
			g := readGeometry(shape.child(slidesNS, "spPr").child(drawingNS, "xfrm"))
			if g.ok {
				picture := cover{name: shapeName(shape), box: g.box(t), z: c.z, picture: true}
				c.shapes.pictures = append(c.shapes.pictures, picture)
				c.shapes.covers = append(c.shapes.covers, picture)
				c.layers = append(c.layers, layer{box: picture.box, state: fillUnknown})
			}
		case "graphicFrame":
			c.z++
			g := readGeometry(shape.child(slidesNS, "xfrm"))
			if !g.ok {
				continue
			}
			box := g.box(t)
			if table := shape.child(drawingNS, "graphic").child(drawingNS, "graphicData").child(drawingNS, "tbl"); table != nil {
				c.table(shape, table, box)
				continue
			}
			// A chart or a diagram: text over it sits on what is not known.
			c.layers = append(c.layers, layer{box: box, state: fillUnknown})
		case "grpSp":
			c.group(shape, t)
		}
	}
}

// group reads a group's children through its transform: their coordinates
// are in the group's child space (chOff, chExt), scaled to its box.
func (c *slideContext) group(group *element, t transform) {
	xfrm := group.child(slidesNS, "grpSpPr").child(drawingNS, "xfrm")
	g := readGeometry(xfrm)
	chOff, chExt := xfrm.child(drawingNS, "chOff"), xfrm.child(drawingNS, "chExt")
	inner := t
	if g.ok && chOff != nil && chExt != nil {
		cx, _ := strconv.ParseFloat(attrOr(chOff, "x", "0"), 64)
		cy, _ := strconv.ParseFloat(attrOr(chOff, "y", "0"), 64)
		cw, _ := strconv.ParseFloat(attrOr(chExt, "cx", "0"), 64)
		ch, _ := strconv.ParseFloat(attrOr(chExt, "cy", "0"), 64)
		kx, ky := 1.0, 1.0
		if cw > 0 {
			kx = g.w / cw
		}
		if ch > 0 {
			ky = g.h / ch
		}
		// child x -> g.x + (x - cx) * kx, then the outer transform.
		inner = transform{sx: t.sx * kx, sy: t.sy * ky, dx: t.dx + t.sx*(g.x-cx*kx), dy: t.dy + t.sy*(g.y-cy*ky)}
	}
	c.walk(group, inner)
}

// shapeName is a shape's name as the deck gives it, for a finding to point
// at ("TextBox 4").
func shapeName(shape *element) string {
	for _, holder := range []string{"nvSpPr", "nvCxnSpPr", "nvPicPr", "nvGraphicFramePr", "nvGrpSpPr"} {
		if properties := shape.child(slidesNS, holder); properties != nil {
			if name := attrOr(properties.child(slidesNS, "cNvPr"), "name", ""); name != "" {
				return name
			}
		}
	}
	return shape.name
}

// placeholder is a shape's placeholder type and index; ok is false for a
// shape that is not one.
func placeholderOf(shape *element) (kind, index string, ok bool) {
	for _, holder := range []string{"nvSpPr", "nvPicPr", "nvGraphicFramePr"} {
		if ph := shape.child(slidesNS, holder).child(slidesNS, "nvPr").child(slidesNS, "ph"); ph != nil {
			return attrOr(ph, "type", "obj"), attrOr(ph, "idx", ""), true
		}
	}
	return "", "", false
}

// inherited is the placeholder a slide's placeholder takes what it does not
// state from: on the layout by index, else by type; on the master by type.
func inherited(owner *element, kind, index string, byIndex bool) *element {
	tree := owner.child(slidesNS, "cSld").child(slidesNS, "spTree")
	if tree == nil {
		return nil
	}
	family := func(k string) string {
		switch k {
		case "ctrTitle", "title":
			return "title"
		case "subTitle", "body", "obj":
			return "body"
		}
		return k
	}
	var byType *element
	var found *element
	tree.each(slidesNS, "sp", func(sp *element) {
		k, i, ok := placeholderOf(sp)
		if !ok || found != nil {
			return
		}
		if byIndex && index != "" && i == index {
			found = sp
		}
		if byType == nil && (k == kind || family(k) == family(kind)) {
			byType = sp
		}
	})
	if found != nil {
		return found
	}
	return byType
}

// shape reads one sp: its text, its fill and, for a line or an unfilled
// freeform, its stroke.
func (c *slideContext) shape(shape *element, t transform) {
	c.z++
	properties := shape.child(slidesNS, "spPr")
	style := shape.child(slidesNS, "style")
	kind, index, isPlaceholder := placeholderOf(shape)
	var fromLayout, fromMaster *element
	if isPlaceholder {
		fromLayout = inherited(c.layout, kind, index, true)
		fromMaster = inherited(c.master, kind, index, false)
	}
	g := readGeometry(properties.child(drawingNS, "xfrm"))
	for _, source := range []*element{fromLayout, fromMaster} {
		if !g.ok && source != nil {
			g = readGeometry(source.child(slidesNS, "spPr").child(drawingNS, "xfrm"))
		}
	}
	if !g.ok {
		return
	}
	box := g.box(t)
	name := shapeName(shape)

	// Its fill: stated, else (for a shape that is not a text box or a
	// placeholder) its style's, which is how python-pptx's autoshapes are
	// accent-filled without a word in spPr.
	state, colourElement := fillOf(properties)
	if state == fillUnset && style != nil && !isPlaceholder {
		if ref := style.child(drawingNS, "fillRef"); ref != nil && attrOr(ref, "idx", "0") != "0" && len(ref.children) > 0 {
			state, colourElement = fillSolid, ref.children[0]
		}
	}
	fill := layer{box: box, state: state, alpha: 1}
	if state == fillSolid {
		hex, alpha, ok := c.theme.colour(colourElement, "")
		if ok {
			fill.hex, fill.alpha = hex, alpha
		} else {
			fill.state = fillUnknown
		}
	}

	custom := properties.child(drawingNS, "custGeom")
	prst := attrOr(properties.child(drawingNS, "prstGeom"), "prst", "")
	if c.lineVisible(properties, style) {
		switch {
		case custom != nil && (state == fillNone || state == fillUnset && style == nil):
			c.shapes.strokes = append(c.shapes.strokes, stroke{name: name, segments: customSegments(custom, g, t)})
		case isLinePreset(prst):
			c.shapes.strokes = append(c.shapes.strokes, stroke{name: name, segments: presetSegments(prst, g, t)})
		}
	}

	// A filled shape hides what it is drawn over, unless it is mostly
	// see-through.
	hides := (fill.state == fillSolid && fill.alpha >= 0.5) || fill.state == fillUnknown
	if hides && !isLinePreset(prst) {
		c.shapes.covers = append(c.shapes.covers, cover{name: name, box: box, z: c.z})
	}

	if body := shape.child(slidesNS, "txBody"); body != nil {
		frame := c.textOf(body, shape, style, fromLayout, fromMaster, kind, isPlaceholder, fill, box)
		frame.name, frame.box, frame.rotated, frame.z = name, box, g.rotated, c.z
		if frame.text != "" {
			c.shapes.frames = append(c.shapes.frames, frame)
		}
	}
	if fill.state != fillNone && fill.state != fillUnset && !isLinePreset(prst) {
		c.layers = append(c.layers, fill)
	}
}

// isLinePreset reports a preset geometry that is a line, not an area.
func isLinePreset(prst string) bool {
	return prst == "line" || prst == "straightConnector1" || strings.HasPrefix(prst, "bentConnector") || strings.HasPrefix(prst, "curvedConnector")
}

// lineVisible reports whether a shape's outline is drawn: its own line
// fill, else its style's line reference.
func (c *slideContext) lineVisible(properties, style *element) bool {
	if line := properties.child(drawingNS, "ln"); line != nil {
		switch state, _ := fillOf(line); state {
		case fillNone:
			return false
		case fillSolid, fillUnknown:
			return true
		}
	}
	if ref := style.child(drawingNS, "lnRef"); ref != nil {
		return attrOr(ref, "idx", "0") != "0"
	}
	return false
}

// presetSegments are the segments a preset line or connector is drawn
// along, in its box (0..1 each way) before flips. A curved connector is
// taken as the bent one it rounds: the same ends and the same turns.
func presetSegments(prst string, g geometry, t transform) [][2]point {
	var unit []point
	switch {
	case prst == "bentConnector2":
		unit = []point{{0, 0}, {1, 0}, {1, 1}}
	case strings.HasPrefix(prst, "bentConnector"), strings.HasPrefix(prst, "curvedConnector"):
		unit = []point{{0, 0}, {0.5, 0}, {0.5, 1}, {1, 1}}
	default:
		unit = []point{{0, 0}, {1, 1}}
	}
	places := make([]point, len(unit))
	for i, u := range unit {
		places[i] = g.place(u.x, u.y, t)
	}
	return polyline(places)
}

// place maps a point in a shape's box (0..1 each way) to the slide,
// flipped as the shape is.
func (g geometry) place(u, v float64, t transform) point {
	if g.flipH {
		u = 1 - u
	}
	if g.flipV {
		v = 1 - v
	}
	return t.apply(g.x+u*g.w, g.y+v*g.h)
}

func polyline(places []point) [][2]point {
	var out [][2]point
	for i := 1; i < len(places); i++ {
		out = append(out, [2]point{places[i-1], places[i]})
	}
	return out
}

// customSegments are a freeform's paths as segments. Curves are followed
// in eight steps, which is well inside the slack the rules allow; an arc
// ends the run of segments it is in (its end is not computed here).
func customSegments(custom *element, g geometry, t transform) [][2]point {
	var out [][2]point
	paths := custom.child(drawingNS, "pathLst")
	if paths == nil {
		return nil
	}
	for _, p := range paths.children {
		if p.name != "path" {
			continue
		}
		pw, _ := strconv.ParseFloat(attrOr(p, "w", "0"), 64)
		ph, _ := strconv.ParseFloat(attrOr(p, "h", "0"), 64)
		if pw <= 0 {
			pw = g.w
		}
		if ph <= 0 {
			ph = g.h
		}
		if pw <= 0 || ph <= 0 {
			continue
		}
		at := func(pt *element) point {
			x, _ := strconv.ParseFloat(attrOr(pt, "x", "0"), 64)
			y, _ := strconv.ParseFloat(attrOr(pt, "y", "0"), 64)
			return g.place(x/pw, y/ph, t)
		}
		points := func(command *element) []point {
			var out []point
			for _, pt := range command.children {
				if pt.name == "pt" {
					out = append(out, at(pt))
				}
			}
			return out
		}
		var current, start point
		have := false
		for _, command := range p.children {
			pts := points(command)
			switch command.name {
			case "moveTo":
				if len(pts) == 1 {
					current, start, have = pts[0], pts[0], true
				}
			case "lnTo":
				if len(pts) == 1 && have {
					out = append(out, [2]point{current, pts[0]})
					current = pts[0]
				}
			case "cubicBezTo", "quadBezTo":
				if !have || len(pts) < 2 {
					have = false
					continue
				}
				control := append([]point{current}, pts...)
				previous := current
				for step := 1; step <= 8; step++ {
					next := bezier(control, float64(step)/8)
					out = append(out, [2]point{previous, next})
					previous = next
				}
				current = pts[len(pts)-1]
			case "arcTo":
				have = false
			case "close":
				if have {
					out = append(out, [2]point{current, start})
					current = start
				}
			}
		}
	}
	return out
}

// bezier is the point at s along a Bézier curve of any order.
func bezier(control []point, s float64) point {
	pts := append([]point{}, control...)
	for len(pts) > 1 {
		for i := 0; i < len(pts)-1; i++ {
			pts[i] = point{pts[i].x + (pts[i+1].x-pts[i].x)*s, pts[i].y + (pts[i+1].y-pts[i].y)*s}
		}
		pts = pts[:len(pts)-1]
	}
	return pts[0]
}

// table reads a table's text: one frame for the whole table (LibreOffice
// grows a row to fit its text, so text past the table's box is a table
// taller than the room it was given).
func (c *slideContext) table(shape, table *element, box rect) {
	frame := textFrame{name: shapeName(shape), box: box, wraps: true, table: true, z: c.z}
	behind := c.under(box)
	var texts []string
	table.each(drawingNS, "tc", func(cell *element) {
		ground := behind
		// A table style shades cells the deck does not state; only a
		// stated fill is a known ground.
		if properties := cell.child(drawingNS, "tcPr"); properties != nil {
			switch state, colour := fillOf(properties); state {
			case fillSolid:
				ground, _, _ = c.theme.colour(colour, "")
			case fillUnknown:
				ground = ""
			}
		} else if table.child(drawingNS, "tblPr").child(drawingNS, "tableStyleId") != nil {
			ground = ""
		}
		body := cell.child(drawingNS, "txBody")
		if body == nil {
			return
		}
		for _, paragraph := range body.children {
			if paragraph.name != "p" {
				continue
			}
			text := c.paragraphRuns(paragraph, &frame, nil, ground, 1)
			if text != "" {
				texts = append(texts, text)
				frame.paragraphs++
			}
		}
	})
	frame.text = strings.Join(texts, " ")
	if frame.text != "" {
		c.shapes.frames = append(c.shapes.frames, frame)
	}
	c.layers = append(c.layers, layer{box: box, state: fillNone})
}

// under is the colour of what a box's middle sits on: the topmost filled
// layer drawn so far that covers it, blended where it is see-through, else
// the slide's background. "" is not known.
func (c *slideContext) under(box rect) string {
	return c.groundAt(box.x+box.w/2, box.y+box.h/2, len(c.layers))
}

func (c *slideContext) groundAt(x, y float64, below int) string {
	for i := below - 1; i >= 0; i-- {
		l := c.layers[i]
		if !l.box.covers(x, y, 0) || l.state == fillNone || l.state == fillUnset {
			continue
		}
		if l.state == fillUnknown {
			return ""
		}
		if l.alpha >= 0.999 {
			return l.hex
		}
		rest := c.groundAt(x, y, i)
		if rest == "" {
			return ""
		}
		return blend(rest, l.hex, l.alpha)
	}
	if !c.backgroundKnown {
		return ""
	}
	return c.background
}

// textOf reads a shape's text and the size, colour and ground of each run.
func (c *slideContext) textOf(body, shape, style, fromLayout, fromMaster *element, kind string, isPlaceholder bool, fill layer, box rect) textFrame {
	var frame textFrame
	properties := body.child(drawingNS, "bodyPr")
	wrap := attrOr(properties, "wrap", "")
	for _, source := range []*element{fromLayout, fromMaster} {
		if wrap == "" && source != nil {
			wrap = attrOr(source.child(slidesNS, "txBody").child(drawingNS, "bodyPr"), "wrap", "")
		}
	}
	frame.wraps = wrap != "none"
	frame.grows = properties.child(drawingNS, "spAutoFit") != nil
	if vertical := attrOr(properties, "vert", "horz"); vertical != "horz" {
		frame.rotated = true
	}
	scale := 1.0
	if shrink := properties.child(drawingNS, "normAutofit"); shrink != nil {
		if value, err := strconv.ParseFloat(attrOr(shrink, "fontScale", "100000"), 64); err == nil && value > 0 {
			scale = value / 100_000
		}
	}

	// What a run's size and colour fall back to, nearest first.
	var styles []*element
	styles = append(styles, body.child(drawingNS, "lstStyle"))
	for _, source := range []*element{fromLayout, fromMaster} {
		if source != nil {
			styles = append(styles, source.child(slidesNS, "txBody").child(drawingNS, "lstStyle"))
		}
	}
	if isPlaceholder {
		textStyles := c.master.child(slidesNS, "txStyles")
		switch kind {
		case "title", "ctrTitle":
			styles = append(styles, textStyles.child(slidesNS, "titleStyle"))
		case "dt", "ftr", "sldNum", "hdr":
			styles = append(styles, textStyles.child(slidesNS, "otherStyle"))
		default:
			styles = append(styles, textStyles.child(slidesNS, "bodyStyle"))
		}
	}
	styles = append(styles, c.defaults)
	var fontColour *element
	if ref := style.child(drawingNS, "fontRef"); ref != nil && len(ref.children) > 0 {
		fontColour = ref.children[0]
	}
	resolver := &runStyles{styles: styles, fontColour: fontColour}

	ground := ""
	switch fill.state {
	case fillSolid:
		ground = fill.hex
		if fill.alpha < 0.999 {
			if behind := c.under(box); behind != "" {
				ground = blend(behind, fill.hex, fill.alpha)
			} else {
				ground = ""
			}
		}
	case fillUnknown:
	default:
		ground = c.under(box)
	}

	var texts []string
	for _, paragraph := range body.children {
		if paragraph.name != "p" || paragraph.space != drawingNS {
			continue
		}
		if text := c.paragraphRuns(paragraph, &frame, resolver, ground, scale); text != "" {
			texts = append(texts, text)
			frame.paragraphs++
		}
	}
	frame.text = strings.TrimSpace(strings.Join(texts, " "))
	return frame
}

// runStyles resolves what a run does not state.
type runStyles struct {
	styles     []*element
	fontColour *element
}

// lookup finds a run property for a paragraph level in the fallback
// styles; pick reads it from one defRPr.
func (r *runStyles) lookup(level int, pick func(*element) bool) {
	if r == nil {
		return
	}
	name := "lvl" + strconv.Itoa(level+1) + "pPr"
	for _, style := range r.styles {
		if style == nil {
			continue
		}
		if properties := style.child(drawingNS, name).child(drawingNS, "defRPr"); properties != nil && pick(properties) {
			return
		}
	}
}

// paragraphRuns reads one paragraph's runs into the frame and returns its
// text.
func (c *slideContext) paragraphRuns(paragraph *element, frame *textFrame, resolver *runStyles, ground string, scale float64) string {
	properties := paragraph.child(drawingNS, "pPr")
	level, _ := strconv.Atoi(attrOr(properties, "lvl", "0"))
	paragraphDefaults := properties.child(drawingNS, "defRPr")
	var text strings.Builder
	for _, run := range paragraph.children {
		if run.name != "r" && run.name != "fld" {
			continue
		}
		words := ""
		if t := run.child(drawingNS, "t"); t != nil {
			words = t.text
		}
		// A field (a date, a slide number) is drawn as LibreOffice
		// computes it, not as its cached text, so it is held to size and
		// contrast but not looked for word by word.
		if run.name == "r" {
			text.WriteString(words)
		}
		if strings.TrimSpace(words) == "" {
			continue
		}
		own := run.child(drawingNS, "rPr")
		size, bold, colour := 0.0, false, ""
		read := func(e *element) {
			if e == nil {
				return
			}
			if size == 0 {
				if v, err := strconv.ParseFloat(attrOr(e, "sz", ""), 64); err == nil && v > 0 {
					size = v / 100
				}
			}
			if !bold {
				bold = attrOr(e, "b", "0") == "1"
			}
			if colour == "" {
				if state, element := fillOf(e); state == fillSolid {
					colour, _, _ = c.theme.colour(element, "")
				}
			}
		}
		read(own)
		read(paragraphDefaults)
		if resolver != nil {
			// The shape's own list style first, then its style's font
			// colour (an autoshape's white text), then the inherited ones.
			if len(resolver.styles) > 0 && resolver.styles[0] != nil {
				first := &runStyles{styles: resolver.styles[:1]}
				first.lookup(level, func(e *element) bool { read(e); return size != 0 && colour != "" })
			}
			if colour == "" && resolver.fontColour != nil {
				colour, _, _ = c.theme.colour(resolver.fontColour, "")
			}
			rest := &runStyles{styles: resolver.styles[1:]}
			rest.lookup(level, func(e *element) bool { read(e); return size != 0 && colour != "" })
		}
		if size == 0 {
			// What PowerPoint sets text in when nothing says.
			size = 18
		}
		if colour == "" && !frame.table {
			colour, _, _ = c.theme.colour(&element{name: "schemeClr", attrs: attrList("val", "tx1")}, "")
		}
		frame.runs = append(frame.runs, textRun{text: words, size: size * scale, bold: bold, colour: colour, ground: ground})
	}
	return strings.TrimSpace(text.String())
}

// attrList is one unprefixed attribute, for an element made here.
func attrList(name, value string) []xml.Attr {
	return []xml.Attr{{Name: xml.Name{Local: name}, Value: value}}
}

// String names a slide's frame for a finding: its name and the start of its
// text.
func (f textFrame) String() string {
	text := strings.Join(strings.Fields(f.text), " ")
	if runes := []rune(text); len(runes) > 60 {
		text = string(runes[:57]) + "..."
	}
	return fmt.Sprintf("%q (%q)", f.name, text)
}
