// Package pictures is what an upload's vision switch does to a document:
// an uploaded picture becomes its description, and a PDF's embedded
// pictures are described and the descriptions put where each one sits in
// the parsed Markdown.
//
// It is the Go form of the Python tier's picture describer
// (etl_pipeline/picture_describer.py, parsers/vision_llm.py). A parser
// reads a PDF's text and drops its pictures: a CT scan in a clinical PDF
// becomes, at best, a placeholder and its caption. With vision on, each
// picture is pulled out of the PDF (pdfcpu), described by the vision model
// (package vision, through the proxy) and spliced into the text, so the
// chunk that holds a question also holds what its picture shows.
//
// # Where a description goes
//
// The parser's page table (parse.Document.Pages) says where each page's
// Markdown is, and pdfcpu says which page each picture is drawn on. So a
// description goes on its own picture's page: in place of that page's
// n-th image marker for its n-th picture (`<!-- image -->` with an optional
// `Image: <name>` caption line, a Markdown image `![…](…)`, a bare
// `Image: <name>` caption), after a layout parser's `<figure>…</figure>`
// (whose own text is kept), and at the end of the page when the page has
// no marker left. A document with no page table is matched against its
// markers as a whole, and what is left is appended under a heading.
//
// Better than the Python: it matched markers across the whole document in
// order, so one page with a picture and no marker (pymupdf4llm emits none)
// pushed every later description one picture too early, and with no
// markers at all every description was appended at the end of the
// document, far from the page it belongs to. Here a page's pictures stay
// on that page, and the page table is rebuilt so every chunk's page
// number stays true.
//
// # What is skipped, and what is said
//
// A picture under minimumBytes (a tracking pixel, a rule, a dot) is not
// worth a model call; the same picture drawn twice (a logo on every page)
// is described once, where it first appears; a picture the vision model
// cannot be sent (JPEG 2000, too large even made smaller) is counted. A
// picture the model failed on is counted with its reason, and the document
// says so in a visible line: it still indexes, with the text it has,
// rather than failing for want of one picture's description.
package pictures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/filekind"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
)

const (
	// minimumBytes: under a kilobyte a picture is decoration. Its pixels
	// cannot be counted without decoding it, and its size is a fair proxy.
	minimumBytes = 1024
	// concurrency is how many pictures of one document are described at
	// once: vision models are rate limited, and four keeps an image-heavy
	// PDF moving without tripping most providers' limits.
	concurrency = 4
	// MaxPDFBytes is the largest PDF whose pictures are extracted. pdfcpu
	// holds the file and its pictures in memory; past this a PDF is indexed
	// with its text and a line saying its pictures were not described.
	MaxPDFBytes = 200 << 20
)

// Describer is the vision model, opened for the document's owner
// (vision.Endpoint).
type Describer = vision.Describer

// pictureExtensions are the uploads read as a picture when vision is on:
// the formats vision.Prepare can send or convert.
var pictureExtensions = []string{".bmp", ".gif", ".heic", ".heif", ".jpeg", ".jpg", ".png", ".tif", ".tiff", ".webp"}

// Extensions lists the picture files an upload with vision on accepts,
// whatever parser was chosen.
func Extensions() []string { return slices.Clone(pictureExtensions) }

// IsPicture reports whether a file of this name is read as a picture when
// vision is on.
func IsPicture(name string) bool {
	return slices.Contains(pictureExtensions, filekind.Extension(name))
}

// IsPDF reports whether a file of this name has embedded pictures to
// describe.
func IsPDF(name string) bool { return filekind.Extension(name) == ".pdf" }

// Picture is an uploaded picture as a document: its description, unpaged.
func Picture(ctx context.Context, describer Describer, name string, data []byte) (parse.Document, error) {
	described, err := describer.Describe(ctx, vision.Picture{Name: name, Data: data}, vision.Whole)
	if err != nil {
		return parse.Document{}, err
	}
	return parse.Unpaged(parse.Vision, described)
}

// Outcome is what describing a document's pictures did.
type Outcome struct {
	Described, TooSmall, Duplicate, Unsendable, Failed int
	// Failure is the first failure's reason, for the document's note.
	Failure string
}

// found is one picture pulled out of the PDF.
type found struct {
	page int
	name string
	data []byte
	// description is filled in by the vision model.
	description string
}

// Describe describes the pictures embedded in pdf and puts the
// descriptions into document, the parser's reading of the same file.
//
// It returns an error only when ctx ends: every other failure is counted
// in the Outcome and said in the document.
func Describe(ctx context.Context, describer Describer, pdf []byte, document parse.Document) (parse.Document, Outcome, error) {
	var outcome Outcome
	if len(pdf) > MaxPDFBytes {
		outcome.Failure = fmt.Sprintf("the PDF is over %d MB, too large to take its pictures out", MaxPDFBytes>>20)
		return withNote(document, note(outcome, true)), outcome, nil
	}
	pictures, err := extract(pdf)
	if err != nil {
		outcome.Failure = "its pictures could not be read out of the PDF"
		return withNote(document, note(outcome, true)), outcome, nil
	}
	eligible := sift(pictures, &outcome)
	if err := describeAll(ctx, describer, eligible, &outcome); err != nil {
		return parse.Document{}, outcome, err
	}
	var described []found
	for _, picture := range eligible {
		if picture.description != "" {
			described = append(described, picture)
		}
	}
	merged := merge(document, described)
	return withNote(merged, note(outcome, false)), outcome, nil
}

// extract is every picture drawn in the PDF, page by page, in object
// order within a page. A page whose pictures cannot be read is passed
// over: one bad picture does not cost the others.
func extract(pdf []byte) ([]found, error) {
	configuration := model.NewDefaultConfiguration()
	configuration.ValidationMode = model.ValidationRelaxed
	configuration.Cmd = model.EXTRACTIMAGES
	context, err := api.ReadValidateAndOptimize(bytes.NewReader(pdf), configuration)
	if err != nil {
		return nil, err
	}
	var out []found
	for page := 1; page <= context.PageCount; page++ {
		images, err := pdfcpu.ExtractPageImages(context, page, false)
		if err != nil {
			continue
		}
		numbers := make([]int, 0, len(images))
		for number := range images {
			numbers = append(numbers, number)
		}
		sort.Ints(numbers)
		for _, number := range numbers {
			image := images[number]
			if image.Thumb || image.Reader == nil {
				continue
			}
			data, err := io.ReadAll(image.Reader)
			if err != nil {
				continue
			}
			name := image.Name
			if name == "" {
				name = fmt.Sprintf("picture%d", number)
			}
			out = append(out, found{page: page, name: name, data: data})
		}
	}
	return out, nil
}

// sift keeps the pictures worth a model call.
func sift(pictures []found, outcome *Outcome) []found {
	seen := map[[sha256.Size]byte]bool{}
	var kept []found
	for _, picture := range pictures {
		if len(picture.data) < minimumBytes {
			outcome.TooSmall++
			continue
		}
		digest := sha256.Sum256(picture.data)
		if seen[digest] {
			outcome.Duplicate++
			continue
		}
		seen[digest] = true
		if _, _, err := vision.Prepare(picture.data); err != nil {
			outcome.Unsendable++
			continue
		}
		kept = append(kept, picture)
	}
	return kept
}

// describeAll has the vision model describe each picture, a few at once.
func describeAll(ctx context.Context, describer Describer, pictures []found, outcome *Outcome) error {
	gate := make(chan struct{}, concurrency)
	failures := make([]error, len(pictures))
	var waiting sync.WaitGroup
	for index := range pictures {
		waiting.Add(1)
		go func() {
			defer waiting.Done()
			select {
			case gate <- struct{}{}:
			case <-ctx.Done():
				failures[index] = ctx.Err()
				return
			}
			defer func() { <-gate }()
			text, err := describer.Describe(ctx, vision.Picture{Name: pictures[index].name, Data: pictures[index].data}, vision.Embedded)
			if err == nil {
				pictures[index].description = strings.TrimSpace(text)
			}
			failures[index] = err
		}()
	}
	waiting.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	for index, err := range failures {
		if err == nil {
			outcome.Described++
			continue
		}
		outcome.Failed++
		if outcome.Failure == "" {
			outcome.Failure = fmt.Sprintf("page %d %s: %s", pictures[index].page, pictures[index].name, reason(err))
		}
	}
	return nil
}

// reason is a failure in words for the document's note: the parse
// error's own message, never an address or a key.
func reason(err error) string {
	var failure *parse.Error
	if errors.As(err, &failure) {
		text := failure.Message
		if len(text) > 200 {
			text = text[:200]
		}
		return text
	}
	return "the vision model could not be reached"
}

// block is one picture's description as Markdown: horizontal rules and a
// bold label, the Python's layout. Raw HTML would be invisible in the
// document viewer, and a blockquote would evict the description's lists
// and tables; rules hold any Markdown the model writes.
func block(picture found) string {
	return "\n\n---\n\n**Embedded image:** `" + strings.ReplaceAll(picture.name, "`", "'") + "` (page " + fmt.Sprint(picture.page) +
		")\n\n**Visual description:**\n\n" + picture.description + "\n\n---\n\n"
}

// The markers a parser leaves where a picture was. A figure is kept and
// the description goes after it (its contents are the parser's own reading
// of the figure, worth keeping); every other marker carries nothing and is
// replaced.
var (
	figureMarker      = regexp.MustCompile(`(?is)<figure\b[^>]*>.*?</figure>`)
	placeholderMarker = regexp.MustCompile(`(?i)<!--\s*image\s*-->(?:[ \t]*\n[ \t]*Image:[ \t]*\S+[ \t]*(?:\n|$))?`)
	markdownImage     = regexp.MustCompile(`!\[[^\]\n]*\]\([^)\n]*\)`)
	captionMarker     = regexp.MustCompile(`(?im)^[ \t]*Image:[ \t]*\S+[ \t]*$`)
)

// marker is one place a description can go: [start, end) is replaced by
// the description, or, for a figure, the description goes at end.
type marker struct {
	start, end int
	keep       bool
}

// markers are a text's markers in document order, overlapping ones
// dropped (a caption that is part of a placeholder match is that match).
func markers(text string) []marker {
	var all []marker
	for _, kind := range []struct {
		pattern *regexp.Regexp
		keep    bool
	}{{figureMarker, true}, {placeholderMarker, false}, {markdownImage, false}, {captionMarker, false}} {
		for _, span := range kind.pattern.FindAllStringIndex(text, -1) {
			all = append(all, marker{start: span[0], end: span[1], keep: kind.keep})
		}
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].start < all[b].start })
	var kept []marker
	for _, candidate := range all {
		if len(kept) > 0 && candidate.start < kept[len(kept)-1].end {
			continue
		}
		kept = append(kept, candidate)
	}
	return kept
}

// splice puts each picture at the next marker of text, in order, and
// returns the text and the pictures there was no marker for.
func splice(text string, pictures []found) (string, []found) {
	places := markers(text)
	if len(places) == 0 || len(pictures) == 0 {
		return text, pictures
	}
	count := min(len(places), len(pictures))
	var out strings.Builder
	at := 0
	for index := range count {
		place := places[index]
		if place.keep {
			out.WriteString(text[at:place.end])
		} else {
			out.WriteString(text[at:place.start])
		}
		out.WriteString(block(pictures[index]))
		at = place.end
	}
	out.WriteString(text[at:])
	return out.String(), pictures[count:]
}

// merge puts the described pictures into the document.
func merge(document parse.Document, pictures []found) parse.Document {
	if len(pictures) == 0 {
		return document
	}
	if len(document.Pages) == 0 {
		text, left := splice(document.Markdown, pictures)
		document.Markdown = appendSection(text, left, len(left) < len(pictures))
		return document
	}
	byPage := map[int][]found{}
	for _, picture := range pictures {
		byPage[picture.page] = append(byPage[picture.page], picture)
	}
	// The page spans are rebuilt as the text grows: what lies between two
	// spans (a separator) is copied as it was, and each page's own text
	// may gain its descriptions.
	var out strings.Builder
	pages := make([]parse.Page, len(document.Pages))
	at := 0
	for index, page := range document.Pages {
		out.WriteString(document.Markdown[at:page.Start])
		text := document.Markdown[page.Start:page.End]
		if mine := byPage[page.Number]; len(mine) > 0 {
			var left []found
			text, left = splice(text, mine)
			for _, picture := range left {
				text = strings.TrimRight(text, "\n") + block(picture)
			}
			delete(byPage, page.Number)
		}
		start := out.Len()
		out.WriteString(text)
		pages[index] = parse.Page{Number: page.Number, Start: start, End: out.Len()}
		at = page.End
	}
	out.WriteString(document.Markdown[at:])
	// A picture on a page the parser did not report (it skipped the page)
	// is not dropped: it goes at the end, said to be unplaced.
	var stray []found
	for _, picture := range pictures {
		if _, unplaced := byPage[picture.page]; unplaced {
			stray = append(stray, picture)
		}
	}
	document.Markdown = appendSection(out.String(), stray, true)
	document.Pages = pages
	return document
}

// appendSection appends the pictures there was no place for, under a
// heading that says whether some were placed and these were not, or none
// could be.
func appendSection(text string, pictures []found, somePlaced bool) string {
	if len(pictures) == 0 {
		return text
	}
	heading := "## Image Content (vision-LLM extracted)"
	if somePlaced {
		heading = "## Image Content (additional, no inline marker found)"
	}
	var out strings.Builder
	// Added to, never trimmed: the last page's span may end at the very
	// end of the text, and cutting its trailing lines would leave the span
	// pointing past it.
	out.WriteString(text + paragraphBreak(text) + heading + "\n")
	for _, picture := range pictures {
		out.WriteString(block(picture))
	}
	return out.String()
}

// note is the visible line about pictures that were not described, or "".
// whole says no picture was looked at at all.
func note(outcome Outcome, whole bool) string {
	switch {
	case whole:
		return "_Note: the pictures in this document were not described: " + outcome.Failure + "._"
	case outcome.Failed > 0:
		return fmt.Sprintf("_Note: %d embedded picture(s) could not be described by the vision model (first error: %s)._", outcome.Failed, outcome.Failure)
	case outcome.Unsendable > 0:
		return fmt.Sprintf("_Note: %d embedded picture(s) are in a format no vision model takes, and were not described._", outcome.Unsendable)
	}
	return ""
}

// withNote is the document with the note after its last page, outside
// every page's span: it is about the document, not one of its pages.
func withNote(document parse.Document, line string) parse.Document {
	if line == "" {
		return document
	}
	document.Markdown += paragraphBreak(document.Markdown) + line + "\n"
	return document
}

// paragraphBreak is what puts a blank line after text, without removing
// any of it.
func paragraphBreak(text string) string {
	switch {
	case text == "" || strings.HasSuffix(text, "\n\n"):
		return ""
	case strings.HasSuffix(text, "\n"):
		return "\n"
	}
	return "\n\n"
}

// TooLarge is the document of a PDF too large to take pictures out of,
// with the line that says its pictures were not described.
func TooLarge(document parse.Document) parse.Document {
	return withNote(document, note(Outcome{Failure: fmt.Sprintf("the PDF is over %d MB, too large to take its pictures out", MaxPDFBytes>>20)}, true))
}
