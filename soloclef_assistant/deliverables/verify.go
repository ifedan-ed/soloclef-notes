package deliverables

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"
)

// Verification: what stands between a file a model's program wrote and an
// artifact a person opens.
//
// A built file is checked in three ways, each catching what the others
// cannot. Its structure is read from its bytes in this process
// (structure.go). It is rendered in the sandbox, by LibreOffice and
// poppler, which shows whether it opens at all, how many pages it really
// has, whether each page has text on it and whether its fonts travel with
// it. And where every word was drawn is held to where it was meant to be
// (layout_rules.go): on the page, over no other word, and for a deck
// inside its box, crossed by no line, hidden by no shape, large enough and
// in contrast with what it sits on. That last check is measurement, not a
// model looking at pictures: it is exact, free and the same every time.
//
// A file that passes is recorded here, by the server: which sandbox, which
// path, the hash of the bytes that were checked, and where the preview that
// was checked sits with its hash. save_artifact stores a file only if this
// record has it and the bytes still hash the same. The record is this
// process's memory: verifying and saving are consecutive steps of one task
// in one process, and a task that is interrupted between them verifies
// again, which is the safe direction to fail in.

// maxVerifyPages is the most pages or slides one artifact may have and be
// verified: a deck or report past it is one to split, and LibreOffice's
// conversion of it would outlast the sandbox's command timeout.
const maxVerifyPages = 40

// minPageChars is the least text a page of a document must carry; fewer is
// a blank or near-blank page, which is nearly always a layout fault (a
// forced break, a table pushed off the page). A slide may be all picture.
const minPageChars = 20

// VerifyResult is what verify_artifact answers: what the model acts on, and
// exactly what the page's verification card draws.
type VerifyResult struct {
	// Status is "verified" or "failed".
	Status string `json:"status"`
	// Findings are the faults to fix; a verified file has none.
	Findings []string `json:"findings"`
	// Notes are advice and warnings; they block nothing.
	Notes     []string `json:"notes"`
	PageCount *int     `json:"page_count"`
}

const (
	statusVerified       = "verified"
	statusNotVerified    = "failed"
	verificationLifetime = time.Hour
	// maxVerifications bounds the record. It is far above what a process
	// holds at once (one per artifact between two consecutive tool calls);
	// reaching it means entries are not being consumed, and the oldest go.
	maxVerifications = 4096
)

// verification is one file that passed.
type verification struct {
	format string
	// primary is the hash of the bytes that were checked.
	primary [sha256.Size]byte
	// previewPath and preview are the PDF the pages were checked on and
	// its hash, for a converted format; previewPath is "" otherwise.
	previewPath string
	preview     [sha256.Size]byte
	// buildDir is the sandbox directory holding the preview, removed once
	// the file is saved.
	buildDir string
	expires  time.Time
}

type verificationKey struct{ sandbox, path string }

// Verifications is the server's record of which sandbox files have passed
// verification. One is shared by every turn of a process.
type Verifications struct {
	mu      sync.Mutex
	entries map[verificationKey]verification
	now     func() time.Time
}

// NewVerifications returns an empty record.
func NewVerifications() *Verifications {
	return &Verifications{entries: map[verificationKey]verification{}, now: time.Now}
}

func (v *Verifications) put(sandboxID, path string, entry verification) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	if len(v.entries) >= maxVerifications {
		for key, old := range v.entries {
			if !old.expires.After(now) {
				delete(v.entries, key)
			}
		}
		// Still full of live entries: drop the ones that expire first.
		for len(v.entries) >= maxVerifications {
			var oldest verificationKey
			var at time.Time
			for key, old := range v.entries {
				if at.IsZero() || old.expires.Before(at) {
					oldest, at = key, old.expires
				}
			}
			delete(v.entries, oldest)
		}
	}
	entry.expires = now.Add(verificationLifetime)
	v.entries[verificationKey{sandboxID, path}] = entry
}

// get returns the record of a file, if it passed and the record is fresh.
func (v *Verifications) get(sandboxID, path string) (verification, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	key := verificationKey{sandboxID, path}
	entry, found := v.entries[key]
	if found && !entry.expires.After(v.now()) {
		delete(v.entries, key)
		return verification{}, false
	}
	return entry, found
}

// forget removes a file's record: before it is verified again, and once it
// has been saved, so one pass stores one artifact.
func (v *Verifications) forget(sandboxID, path string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.entries, verificationKey{sandboxID, path})
}

// The lines the render command prints. Everything else it prints is
// ignored.
const (
	markFailed = "SOLOCLEF_FAILED="
	markHash   = "SOLOCLEF_SHA="
	markPages  = "SOLOCLEF_PAGES="
	markChars  = "SOLOCLEF_CHARS="
	markFont   = "SOLOCLEF_FONT="
)

// wordsFile is where in the build directory the render command lists every
// drawn word with its box, for the placement rules.
const wordsFile = "words.html"

// renderCommand is the one command that renders a file and measures the
// result. It works on a copy in a directory of its own, so the file the
// model's code may still touch is not what is measured, and prints the
// copy's hash so this side can tell that the copy is the file it read.
//
// One command, not a dozen: each round trip to the sandbox costs more than
// most of these steps do.
func renderCommand(build, source string, format documentFormat) string {
	var b strings.Builder
	b.WriteString("build=" + quoted(build) + "; src=" + quoted(source) + "; copy=\"$build/primary" + format.suffix + "\"; pdf=\"$build/primary.pdf\"\n")
	b.WriteString(`fail() { echo "` + markFailed + `$1"; rm -rf -- "$build"; exit 0; }` + "\n")
	b.WriteString(`mkdir -p -- "$build" && cp -- "$src" "$copy" || fail copying` + "\n")
	b.WriteString(`echo "` + markHash + `$(sha256sum "$copy" | cut -d' ' -f1)"` + "\n")
	if format.converted {
		// A profile directory of its own: two conversions sharing
		// LibreOffice's default profile lock each other out.
		b.WriteString(`soffice --headless "-env:UserInstallation=file://$build/profile" --convert-to pdf --outdir "$build" "$copy" >/dev/null 2>&1; test -s "$pdf" || fail converting` + "\n")
	}
	b.WriteString(`pages=$(pdfinfo "$pdf" 2>/dev/null | sed -n 's/^Pages: *//p')` + "\n")
	b.WriteString(`case "$pages" in ''|*[!0-9]*) fail reading;; esac` + "\n")
	b.WriteString(`echo "` + markPages + `$pages"` + "\n")
	// Nothing is rasterised for a document over the limit.
	b.WriteString(`[ "$pages" -ge 1 ] && [ "$pages" -le ` + strconv.Itoa(maxVerifyPages) + ` ] || exit 0` + "\n")
	b.WriteString(`p=1; while [ "$p" -le "$pages" ]; do echo "` + markChars + `$p $(pdftotext -f "$p" -l "$p" "$pdf" - 2>/dev/null | tr -d '[:space:]' | wc -m)"; p=$((p+1)); done` + "\n")
	// pdffonts prints two header lines, then a row per font whose fourth
	// column from the end of the fixed ones is "emb". A font's type may be
	// two words, so the columns are counted from the right.
	b.WriteString(`pdffonts "$pdf" 2>/dev/null | awk 'NR>2 && NF>=6 && $(NF-4)=="no" {print "` + markFont + `" $1}'` + "\n")
	// Every word with its box, read back here: what the placement rules
	// measure. No page is rasterised: nothing looks at pictures of it.
	b.WriteString(`pdftotext -bbox-layout "$pdf" "$build/` + wordsFile + `" >/dev/null 2>&1 || fail measuring` + "\n")
	return b.String()
}

// rendering is what the render command reported.
type rendering struct {
	failed string
	hash   string
	pages  int
	// chars is the count of non-space characters on each page, by page.
	chars map[int]int
	fonts []string
}

func readRendering(output string) rendering {
	out := rendering{chars: map[int]int{}}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, markFailed):
			out.failed = strings.TrimPrefix(line, markFailed)
		case strings.HasPrefix(line, markHash):
			out.hash = strings.TrimPrefix(line, markHash)
		case strings.HasPrefix(line, markPages):
			out.pages, _ = strconv.Atoi(strings.TrimPrefix(line, markPages))
		case strings.HasPrefix(line, markChars):
			page, count, _ := strings.Cut(strings.TrimPrefix(line, markChars), " ")
			number, errPage := strconv.Atoi(page)
			characters, errCount := strconv.Atoi(strings.TrimSpace(count))
			if errPage == nil && errCount == nil {
				out.chars[number] = characters
			}
		case strings.HasPrefix(line, markFont):
			out.fonts = append(out.fonts, strings.TrimPrefix(line, markFont))
		}
	}
	return out
}

// renderFindings is what the rendering shows to be wrong. expectedPages is
// 0 when the format declares no count.
func renderFindings(r rendering, format documentFormat, expectedPages int) []string {
	var findings []string
	if expectedPages > 0 && r.pages != expectedPages {
		findings = append(findings, fmt.Sprintf("expected %d page(s), found %d", expectedPages, r.pages))
	}
	if !format.slides {
		for page := 1; page <= r.pages; page++ {
			if count := r.chars[page]; count < minPageChars {
				findings = append(findings, fmt.Sprintf("page %d is blank or near-blank (%d non-whitespace characters)", page, count))
			}
		}
	}
	if fonts := distinct(r.fonts); len(fonts) > 0 {
		findings = append(findings, "the document uses fonts that are not embedded, so it will look different elsewhere: "+strings.Join(fonts, ", "))
	}
	return findings
}

func (s Specialist) verifyTool(owner Owner) tool.Tool {
	return tool.Tool{
		Name:        VerifyTool,
		Description: "Check a file built in the sandbox before it is saved: its structure, how it renders, and for a pdf, docx or pptx where its text is drawn (on the page, over no other text; for a deck also inside its box, crossed by no line, hidden by no shape, at least 12 pt with bodies at least 16 pt, and in WCAG contrast with its ground). It answers status \"verified\", or \"failed\" with every finding to fix. Only a verified file can be saved, and changing the file afterwards means verifying it again.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"The file's absolute path in the sandbox."},"format":{"type":"string","enum":["pdf","docx","pptx","xlsx","html"]}},"required":["path","format"]}`),
		Approval:    tool.Auto,
		Run: func(ctx context.Context, call tool.Call) (string, error) {
			var args struct {
				Path   string `json:"path"`
				Format string `json:"format"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return "", fmt.Errorf("the arguments are not readable: %w", err)
			}
			name := strings.ToLower(strings.TrimSpace(args.Format))
			format, known := documentFormats[name]
			if !known {
				return "", fmt.Errorf("format %q is not checked by %s; the formats that are: %s", args.Format, VerifyTool, documentFormatNames)
			}
			where, err := sandboxPath(args.Path)
			if err != nil {
				return "", err
			}
			if !strings.HasSuffix(strings.ToLower(where), format.suffix) {
				return "", fmt.Errorf("a %s artifact is a %s file; %q is not", name, format.suffix, where)
			}
			bench, err := s.bench(ctx, owner)
			if err != nil {
				return "", err
			}
			result, err := s.verify(ctx, owner, bench, name, format, where)
			if err != nil {
				return "", s.sandboxFailure(ctx, owner, "verifying the file", err)
			}
			out, err := json.Marshal(result)
			return string(out), err
		},
	}
}

// failedVerification is a result with findings.
func failedVerification(pages int, notes []string, findings ...string) VerifyResult {
	result := VerifyResult{Status: statusNotVerified, Findings: findings, Notes: notes}
	if result.Notes == nil {
		// The card reads both as lists.
		result.Notes = []string{}
	}
	if pages > 0 {
		result.PageCount = &pages
	}
	return result
}

// verify checks one file. An error is the sandbox failing; everything about
// the file is a VerifyResult.
func (s Specialist) verify(ctx context.Context, owner Owner, bench Bench, name string, format documentFormat, where string) (VerifyResult, error) {
	// Whatever was recorded for this path is void from here: the file is
	// being checked again because it may have changed.
	s.Verified.forget(bench.ID(), where)

	data, err := bench.ReadFile(ctx, where, MaxDocumentBytes)
	switch {
	case errors.Is(err, sandbox.ErrNotFound):
		return failedVerification(0, nil, "The artifact file is missing. Generate it again before verification."), nil
	case errors.Is(err, sandbox.ErrTooLarge):
		return failedVerification(0, nil, fmt.Sprintf("Artifact is larger than the limit of %d bytes", MaxDocumentBytes)), nil
	case err != nil:
		return VerifyResult{}, err
	}
	checked := format.check(data)
	if len(checked.findings) > 0 {
		return failedVerification(checked.pages, checked.notes, checked.findings...), nil
	}
	if checked.pages > maxVerifyPages {
		return failedVerification(checked.pages, checked.notes, fmt.Sprintf("Document has %d pages; verification supports at most %d", checked.pages, maxVerifyPages)), nil
	}
	passed := verification{format: name, primary: sha256.Sum256(data)}
	if !format.rendered {
		s.Verified.put(bench.ID(), where, passed)
		return VerifyResult{Status: statusVerified, Findings: []string{}, Notes: append([]string{}, checked.notes...)}, nil
	}

	build := "/tmp/soloclef-verify-" + s.Saver.NewID()
	// Removed on every path that does not leave a preview to be saved.
	keep := false
	defer func() {
		if !keep {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			_, _ = bench.Run(cleanup, "rm -rf -- "+quoted(build))
		}
	}()
	ran, err := bench.Run(ctx, renderCommand(build, where, format))
	if err != nil {
		return VerifyResult{}, err
	}
	rendered := readRendering(ran.Output)
	switch {
	case ran.TimedOut:
		return failedVerification(checked.pages, checked.notes, "Rendering the artifact took too long; make it smaller or simpler."), nil
	case rendered.failed != "" || !ran.OK():
		step := rendered.failed
		if step == "" {
			step = "rendering"
		}
		return failedVerification(checked.pages, checked.notes, map[string]string{
			"copying":    "The artifact file could not be read for rendering.",
			"converting": "LibreOffice could not convert the artifact to PDF; the file is not a valid " + name + " document.",
			"reading":    "The PDF could not be parsed.",
			"rendering":  "The pages could not be rendered.",
			"measuring":  "The positions of the drawn text could not be read from the PDF.",
		}[step]), nil
	case rendered.hash != hex.EncodeToString(passed.primary[:]):
		return failedVerification(checked.pages, checked.notes, "The artifact changed while it was being verified. Verify it again when nothing is writing to it."), nil
	case rendered.pages < 1:
		return failedVerification(0, checked.notes, "The document has no pages."), nil
	case rendered.pages > maxVerifyPages:
		return failedVerification(rendered.pages, checked.notes, fmt.Sprintf("Document has %d pages; verification supports at most %d", rendered.pages, maxVerifyPages)), nil
	}
	expected := 0
	if format.slides {
		expected = checked.pages
	}
	if findings := renderFindings(rendered, format, expected); len(findings) > 0 {
		return failedVerification(rendered.pages, checked.notes, findings...), nil
	}
	placement, err := s.placement(ctx, bench, build, data, format, rendered.pages)
	if err != nil {
		return VerifyResult{}, err
	}
	if len(placement) > 0 {
		return failedVerification(rendered.pages, checked.notes, placement...), nil
	}
	notes := append([]string{}, checked.notes...)

	if format.converted {
		// The PDF the pages were rendered from is the artifact's preview.
		// It is read out now and hashed here, so that what is saved later
		// is provably what was checked.
		passed.previewPath, passed.buildDir = build+"/primary.pdf", build
		preview, err := bench.ReadFile(ctx, passed.previewPath, MaxDocumentBytes)
		if errors.Is(err, sandbox.ErrTooLarge) {
			return failedVerification(rendered.pages, notes, fmt.Sprintf("The document's PDF rendering is larger than the limit of %d bytes", MaxDocumentBytes)), nil
		}
		if err != nil {
			return VerifyResult{}, err
		}
		passed.preview = sha256.Sum256(preview)
		keep = true
		// The word list and LibreOffice's profile are not needed again.
		_, _ = bench.Run(ctx, `rm -rf -- `+quoted(build+"/profile")+` `+quoted(build+"/"+wordsFile))
	}
	s.Verified.put(bench.ID(), where, passed)
	return VerifyResult{Status: statusVerified, Findings: []string{}, Notes: notes, PageCount: &rendered.pages}, nil
}

// placement reads back where the render drew every word and holds it to
// the placement rules. An error is the sandbox failing; a word list that
// cannot be read is a finding, since nothing was measured.
func (s Specialist) placement(ctx context.Context, bench Bench, build string, data []byte, format documentFormat, pages int) ([]string, error) {
	listed, err := bench.ReadFile(ctx, build+"/"+wordsFile, maxWordsBytes)
	switch {
	case errors.Is(err, sandbox.ErrNotFound):
		return []string{"The positions of the drawn text could not be read from the PDF."}, nil
	case errors.Is(err, sandbox.ErrTooLarge):
		return []string{fmt.Sprintf("The document carries more text than its placement can be checked for (over %d bytes of words); split it", maxWordsBytes)}, nil
	case err != nil:
		return nil, err
	}
	drawn, err := readPDFWords(listed)
	if err != nil || len(drawn) != pages {
		return []string{"The positions of the drawn text could not be read from the PDF."}, nil
	}
	if !format.slides {
		return layoutFindings(drawn, nil), nil
	}
	deck, err := readDeckShapes(data)
	if err != nil {
		// checkPPTX read this deck already; a part it did not need is
		// unreadable.
		return []string{"The deck's shapes could not be read back: " + err.Error()}, nil
	}
	return layoutFindings(drawn, &deck), nil
}

// unexpandedShell is a whole value that is a shell expansion or a sandbox
// path: what a model writes when it means "the contents of that file"
// ($(cat /workspace/md_rep.md), `cat x`, $MD, /workspace/x.md). The tool's
// arguments are JSON and no shell ever sees them, so such a value would be
// stored as its characters (a live deck's Markdown once was exactly
// "$(cat /workspace/md_rep.md)"). Only the whole value is matched: a
// document about shell may quote any of these in its text.
var unexpandedShell = regexp.MustCompile(`^(?:\$\([^()]*\)|` + "`[^`]+`" + `|\$\{?[A-Za-z_][A-Za-z0-9_]*\}?|/(?:workspace|tmp|home|opt)/\S+)$`)

// unexpanded reports a markdown representation that is not text but a way
// to get it.
func unexpanded(markdown string) bool {
	return unexpandedShell.MatchString(strings.TrimSpace(markdown))
}

// notVerifiedYet is what a save of an unverified file is told.
const notVerifiedYet = VerifyTool + " has not passed this file, or the file was verified too long ago. Call " + VerifyTool + " with this path and format, fix what it finds, and save when it answers \"verified\"."

// saveDocument stores a built document: the bytes at path, if they are the
// bytes that passed verification.
func (s Specialist) saveDocument(ctx context.Context, owner Owner, toolCallID string, args saveArguments) (string, error) {
	var problems []string
	title := checkTitle(&problems, args.Title)
	if args.Cards != nil || args.Questions != nil || args.Markdown != nil {
		problems = append(problems, "format "+args.Format+" is saved from path and markdown_representation; remove cards, questions and markdown")
	}
	where := ""
	if args.Path == nil {
		problems = append(problems, "path is missing: the file in the sandbox, as it was passed to "+VerifyTool)
	} else if clean, err := sandboxPath(*args.Path); err != nil {
		problems = append(problems, err.Error())
	} else {
		where = clean
	}
	markdown := ""
	if args.Representation != nil {
		markdown = strings.TrimSpace(lineEndings.Replace(*args.Representation))
	}
	if markdown == "" {
		problems = append(problems, "markdown_representation is missing: the document's substantive content as Markdown")
	} else if bad := badCharacter(markdown); bad != "" {
		problems = append(problems, "markdown_representation contains "+bad)
	} else if unexpanded(markdown) {
		problems = append(problems, "markdown_representation is "+strconv.Quote(clip(markdown))+", a shell expression or a file path: nothing expands it, so it would be stored as those characters. Pass the Markdown text itself (read the file first if it is in one)")
	}
	if len(problems) > 0 {
		return "", refusal("the "+args.Format+" was not saved", problems)
	}
	bench, err := s.bench(ctx, owner)
	if err != nil {
		return "", err
	}
	passed, found := s.Verified.get(bench.ID(), where)
	if !found {
		return "", errors.New(notVerifiedYet)
	}
	if passed.format != args.Format {
		return "", fmt.Errorf("this file was verified as %s, not %s; save it as %s, or verify it as %s", passed.format, args.Format, passed.format, args.Format)
	}
	changed := errors.New("the file changed after it was verified. Verify it again, then save.")
	data, err := bench.ReadFile(ctx, where, MaxDocumentBytes)
	switch {
	case errors.Is(err, sandbox.ErrNotFound), errors.Is(err, sandbox.ErrTooLarge):
		return "", changed
	case err != nil:
		return "", s.sandboxFailure(ctx, owner, "reading the file to save", err)
	case sha256.Sum256(data) != passed.primary:
		return "", changed
	}
	var preview []byte
	if passed.previewPath != "" {
		preview, err = bench.ReadFile(ctx, passed.previewPath, MaxDocumentBytes)
		switch {
		case errors.Is(err, sandbox.ErrNotFound), errors.Is(err, sandbox.ErrTooLarge):
			return "", changed
		case err != nil:
			return "", s.sandboxFailure(ctx, owner, "reading the preview to save", err)
		case sha256.Sum256(preview) != passed.preview:
			return "", changed
		}
	}
	saved, err := s.Saver.SaveDocument(ctx, owner, toolCallID, BuiltDocument{
		Format: args.Format, Title: title, Markdown: markdown,
		Data: data, Preview: preview,
	})
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		s.Saver.Logger.ErrorContext(ctx, "a built document could not be saved", "error", err.Error(), "user_id", owner.UserID, "format", args.Format)
		return "", errors.New("the artifact could not be saved because of a storage error; it was not saved")
	}
	// One pass stores one artifact; the preview's directory has done its
	// work. A failure to tidy the sandbox is not a failure of the save.
	s.Verified.forget(bench.ID(), where)
	if passed.buildDir != "" {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		_, _ = bench.Run(cleanup, "rm -rf -- "+quoted(passed.buildDir))
	}
	out, err := json.Marshal(saved)
	return string(out), err
}

// verifyCard is the page's verification card: VerifyResult's own fields,
// and nothing a later version of the tool might add to its result.
func verifyCard(text string) (VerifyResult, bool) {
	var result VerifyResult
	if json.Unmarshal([]byte(text), &result) != nil || (result.Status != statusVerified && result.Status != statusNotVerified) {
		return VerifyResult{}, false
	}
	if result.Findings == nil {
		result.Findings = []string{}
	}
	if result.Notes == nil {
		result.Notes = []string{}
	}
	return result, true
}
