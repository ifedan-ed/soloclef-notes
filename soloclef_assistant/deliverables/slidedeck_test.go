package deliverables

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_studio/deckrender"
	"git.danvics.com/danvics/soloclef/soloclef_studio/deckspec"
	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"
)

// sampleDeck is the renderer's sample: a deck with a slide of every kind
// and three sources, as a model would write it.
func sampleDeck(t testing.TB) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("../../soloclef_studio/deckrender/testdata/photosynthesis.json")
	if err != nil {
		t.Fatal(err)
	}
	var deck map[string]any
	if err := json.Unmarshal(raw, &deck); err != nil {
		t.Fatal(err)
	}
	return deck
}

func deckCall(t testing.TB, deck any) string {
	t.Helper()
	args, err := json.Marshal(map[string]any{"format": "pptx", "title": "Photosynthesis", "deck": deck})
	if err != nil {
		t.Fatal(err)
	}
	return string(args)
}

// The sample's slides, and the sources slide drawn after them.
const sampleSlides = 12

func TestADeckIsDrawnFromItsContentAndSaved(t *testing.T) {
	_, l, tools := built(t, "/workspace/other.txt", []byte("kept"))
	l.shed.render.pages = sampleSlides
	text, err := runTool(t, tools[SaveTool], deckCall(t, sampleDeck(t)))
	if err != nil {
		t.Fatal(err)
	}
	var saved Saved
	if err := json.Unmarshal([]byte(text), &saved); err != nil || saved.Format != FormatPPTX || len(saved.Files) != 2 || saved.Files[1].Role != "preview" {
		t.Fatalf("saved: %s, %v", text, err)
	}
	if len(l.created) != 1 {
		t.Fatalf("%d artifacts stored", len(l.created))
	}
	stored := l.created[0]
	if !bytes.HasPrefix(stored.Data, []byte("PK")) || !strings.HasSuffix(stored.Filename, ".pptx") || !bytes.HasPrefix(stored.Preview, []byte("%PDF")) {
		t.Errorf("stored %s, %d bytes, preview %.10q", stored.Filename, len(stored.Data), stored.Preview)
	}
	if checked := checkPPTX(stored.Data); len(checked.findings) > 0 || checked.pages != sampleSlides {
		t.Errorf("the stored file reads back with %v and %d slides", checked.findings, checked.pages)
	}
	for _, want := range []string{"# Photosynthesis: how plants turn light into food", "## 4. The light reactions make ATP and NADPH", "1. Light absorbed: Chlorophyll", "| First CO2 fixed by | Rubisco |", "## Sources"} {
		if !strings.Contains(stored.Markdown, want) {
			t.Errorf("the deck's text has no %q", want)
		}
	}
	// The fake sandbox draws no words, so the layout rules cannot place
	// any here (TestDrawnDecksPassTheLayoutRulesInLibreOffice runs them on
	// what LibreOffice really draws); the deck is kept all the same, marked.
	if !strings.Contains(string(stored.Metadata), `"verified":false`) {
		t.Errorf("metadata %s", stored.Metadata)
	}
	// One conversion, and nothing of it left in the sandbox.
	renders := 0
	for _, command := range l.shed.ran {
		if strings.Contains(command, markPages) {
			renders++
		}
	}
	if renders != 1 || len(l.shed.files) != 1 {
		t.Errorf("%d renders; the sandbox holds %d files afterwards", renders, len(l.shed.files))
	}
}

// clinicalDeck is the renderer's clinical regression deck as a chat's
// model would write it: no narration, which only a video's slides have.
func clinicalDeck(t testing.TB) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("../../soloclef_studio/deckrender/testdata/asthma.json")
	if err != nil {
		t.Fatal(err)
	}
	var deck map[string]any
	if err := json.Unmarshal(raw, &deck); err != nil {
		t.Fatal(err)
	}
	for _, slide := range deck["slides"].([]any) {
		delete(slide.(map[string]any), "narration")
	}
	return deck
}

// The deck a video failed on, as a chat's deck: figures that are words, a
// long institution's name, a rate written as a change, an address wider
// than the slide. It is drawn and saved, not sent back.
func TestAClinicalDeckIsDrawnAndSaved(t *testing.T) {
	_, l, tools := built(t, "/workspace/other.txt", []byte("kept"))
	l.shed.render.pages = 10
	text, err := runTool(t, tools[SaveTool], deckCall(t, clinicalDeck(t)))
	if err != nil {
		t.Fatal(err)
	}
	var saved Saved
	if err := json.Unmarshal([]byte(text), &saved); err != nil || saved.Format != FormatPPTX || len(l.created) != 1 {
		t.Fatalf("saved: %s, %v", text, err)
	}
	if checked := checkPPTX(l.created[0].Data); len(checked.findings) > 0 || checked.pages != 10 {
		t.Errorf("the stored file reads back with %v and %d slides", checked.findings, checked.pages)
	}
	for _, want := range []string{"11.2 → 4.6 per 1000", "Bronchodilators", "NYC Health + Hospitals"} {
		if !strings.Contains(l.created[0].Markdown, want) {
			t.Errorf("the deck's text has no %q", want)
		}
	}
}

func TestADeckIsSentBackWithEverythingToFixAtOnce(t *testing.T) {
	deck := func(change func(map[string]any)) map[string]any {
		d := sampleDeck(t)
		change(d)
		return d
	}
	slides := func(d map[string]any) []any { return d["slides"].([]any) }
	long := strings.Repeat("a point that goes on ", 30)
	for name, test := range map[string]struct {
		args string
		says []string
	}{
		"no deck": {`{"format":"pptx","title":"T"}`, []string{"deck is missing"}},
		"a file's fields": {`{"format":"pptx","title":"T","deck":{},"path":"/workspace/d.pptx","markdown_representation":"x"}`,
			[]string{"a deck is saved from deck alone"}},
		"a field nobody draws": {deckCall(t, deck(func(d map[string]any) { slides(d)[1].(map[string]any)["image"] = "x.png" })),
			[]string{`unknown field "image"`}},
		"every problem of the content": {deckCall(t, deck(func(d map[string]any) {
			d["theme"] = "neon"
			slides(d)[1].(map[string]any)["kind"] = "poster"
			slides(d)[3].(map[string]any)["steps"] = []any{map[string]any{"label": "Only one"}}
			slides(d)[7].(map[string]any)["cites"] = []any{9}
		})), []string{`theme "neon" is not a theme`, `slide 2: kind "poster" is not a slide kind`, "slide 4 (diagram) needs 3 to 6 steps; it has 1", "slide 8 (stats) cites source 9, and the deck lists 3 sources"}},
		"text that cannot fit": {deckCall(t, deck(func(d map[string]any) {
			// Five rows of three cells, each at the longest a cell may be:
			// within every limit, and still more than the grid can hold.
			cell := long[:99]
			row := map[string]any{"aspect": "Aspect", "cells": []any{cell, cell, cell}}
			slides(d)[6].(map[string]any)["rows"] = []any{row, row, row, row, row}
		})), []string{"slide 7 (comparison) row 1 cell 1: there is too much text for its place; cut it by about"}},
		"a word that fits no arrangement": {deckCall(t, deck(func(d map[string]any) {
			slides(d)[7].(map[string]any)["stats"].([]any)[1].(map[string]any)["value"] = "Electroencephalographies"
		})), []string{`slide 8 (stats) stat 2's value: the word "Electroencephalographies" is too wide for its line even at 36 pt`, "room for about"}},
	} {
		_, l, tools := built(t, "/workspace/x", nil)
		_, err := runTool(t, tools[SaveTool], test.args)
		if err == nil {
			t.Errorf("%s: saved", name)
			continue
		}
		for _, says := range test.says {
			if !strings.Contains(err.Error(), says) {
				t.Errorf("%s: %v\nhas no %q", name, err, says)
			}
		}
		// Refused before anything is drawn or converted.
		if len(l.created) != 0 || len(l.shed.ran) != 0 {
			t.Errorf("%s: stored %d, ran %d commands", name, len(l.created), len(l.shed.ran))
		}
	}
}

func TestADeckTheLayoutRulesFaultIsKeptMarkedUnverified(t *testing.T) {
	_, l, tools := built(t, "/workspace/x", nil)
	l.shed.render.pages = sampleSlides
	l.shed.render.words = "<doc>" + `<page width="960" height="540"><flow><block><line><word xMin="900" yMin="10" xMax="990" yMax="40">Overview</word></line></block></flow></page>` +
		strings.Repeat(`<page width="960" height="540"></page>`, sampleSlides-1) + "</doc>"
	if _, err := runTool(t, tools[SaveTool], deckCall(t, sampleDeck(t))); err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Verification struct {
			Verified bool   `json:"verified"`
			Reason   string `json:"reason"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(l.created[0].Metadata, &metadata); err != nil || metadata.Verification.Verified ||
		!strings.Contains(metadata.Verification.Reason, `slide 1: text runs off the slide and is cut: "Overview"`) {
		t.Errorf("metadata %s, %v", l.created[0].Metadata, err)
	}
}

func TestADeckWhosePreviewCannotBeDrawnIsNotSaved(t *testing.T) {
	_, l, tools := built(t, "/workspace/x", nil)
	l.shed.render.failed = "converting"
	if _, err := runTool(t, tools[SaveTool], deckCall(t, sampleDeck(t))); err == nil || !strings.Contains(err.Error(), "preview could not be drawn") || len(l.created) != 0 {
		t.Errorf("%v, stored %d", err, len(l.created))
	}
	_, l, tools = built(t, "/workspace/x", nil)
	l.shed.refuse = &WorkshopRefusal{Reason: noSandboxSentence}
	if _, err := runTool(t, tools[SaveTool], deckCall(t, sampleDeck(t))); err == nil || !strings.Contains(err.Error(), "slide decks") || len(l.created) != 0 {
		t.Errorf("no sandbox: %v, stored %d", err, len(l.created))
	}
}

// hostBench runs the sandbox's commands on this machine, under a
// directory of its own: what LibreOffice and poppler draw, without a
// sandbox service.
type hostBench struct{ root string }

func (h hostBench) Check() error                                             { return nil }
func (h hostBench) Bench(context.Context, Owner, SandboxWait) (Bench, error) { return h, nil }
func (h hostBench) Release(context.Context, Owner) error                     { return nil }
func (h hostBench) ID() string                                               { return "host" }
func (h hostBench) local(path string) string                                 { return filepath.Join(h.root, path) }
func (h hostBench) Run(ctx context.Context, command string) (sandbox.Result, error) {
	command = strings.ReplaceAll(command, "'/tmp/", "'"+h.root+"/tmp/")
	out, err := exec.CommandContext(ctx, "bash", "-c", command).CombinedOutput()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code, err = exit.ExitCode(), nil
	}
	return sandbox.Result{Output: string(out), ExitCode: code}, err
}
func (h hostBench) ReadFile(_ context.Context, path string, limit int64) ([]byte, error) {
	data, err := os.ReadFile(h.local(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, sandbox.ErrNotFound
	}
	if int64(len(data)) > limit {
		return nil, sandbox.ErrTooLarge
	}
	return data, err
}
func (h hostBench) WriteFile(_ context.Context, path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(h.local(path)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(h.local(path), data, 0o644)
}

// The layout rules, run on what LibreOffice draws of every theme's sample
// deck, find nothing: the renderer's measurements hold in the program the
// preview is made with. Needs soffice and pdftotext on this machine, with
// the Carlito and Caladea fonts the sandbox image installs;
// SOLOCLEF_TEST_KEEP_PREVIEWS keeps each preview.
func TestDrawnDecksPassTheLayoutRulesInLibreOffice(t *testing.T) {
	for _, program := range []string{"soffice", "pdftotext", "pdfinfo"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Skipf("%s is not installed here", program)
		}
	}
	if out, _ := exec.Command("fc-list", ":family=Carlito").Output(); len(bytes.TrimSpace(out)) == 0 {
		t.Skip("the Carlito font is not installed here")
	}
	l := &ledger{}
	s, _ := specialist(t, modeltest.NewScript(), l)
	s.Workshop = hostBench{root: t.TempDir()}
	// The sample, and the clinical deck: its figures in wider cards and an
	// address broken with explicit line breaks, which LibreOffice must
	// draw where they were measured.
	clinical, err := json.Marshal(clinicalDeck(t))
	if err != nil {
		t.Fatal(err)
	}
	sample, err := os.ReadFile("../../soloclef_studio/deckrender/testdata/photosynthesis.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, theme := range deckspec.Themes {
		for _, source := range []struct {
			name string
			raw  []byte
		}{{"sample", sample}, {"clinical", clinical}} {
			raw := source.raw
			t.Run(theme.ID+"/"+source.name, func(t *testing.T) {
				deck, err := deckspec.Decode(raw)
				if err != nil {
					t.Fatal(err)
				}
				deck.Theme = theme.ID
				if deck, err = deckspec.Check(deck, deckspec.Options{MinSlides: deckMinSlides, MaxSlides: deckMaxSlides}); err != nil {
					t.Fatal(err)
				}
				scenes, problems := deckrender.Layout(deck)
				if len(problems) > 0 {
					t.Fatal(problems)
				}
				data, err := deckrender.PPTX(deck.Title, scenes, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				started := time.Now()
				preview, findings, err := s.previewDeck(context.Background(), Owner{UserID: "u", ThreadID: 1}, data, len(scenes))
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("converted and measured in %s", time.Since(started).Round(time.Millisecond))
				for _, f := range findings {
					t.Error(f)
				}
				if dir := os.Getenv("SOLOCLEF_TEST_KEEP_PREVIEWS"); dir != "" {
					_ = os.WriteFile(filepath.Join(dir, theme.ID+"-"+source.name+".pdf"), preview, 0o644)
					_ = os.WriteFile(filepath.Join(dir, theme.ID+"-"+source.name+".pptx"), data, 0o644)
				}
			})
		}
	}
}

// The rules still catch what a badly built deck does, in a real
// LibreOffice: the faulty deck a model's python-pptx program once made
// (testdata/faulty-deck.pptx, built with python-pptx 1.0.2 from the
// program this package's tests used to run in the sandbox).
func TestTheLayoutRulesStillCatchAFaultyDeck(t *testing.T) {
	for _, program := range []string{"soffice", "pdftotext", "pdfinfo"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Skipf("%s is not installed here", program)
		}
	}
	data, err := os.ReadFile("testdata/faulty-deck.pptx")
	if err != nil {
		t.Fatal(err)
	}
	l := &ledger{}
	s, _ := specialist(t, modeltest.NewScript(), l)
	s.Workshop = hostBench{root: t.TempDir()}
	_, findings, err := s.previewDeck(context.Background(), Owner{UserID: "u", ThreadID: 1}, data, 2)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(findings, "\n")
	for _, want := range []string{
		`slide 1: the line "Staircase" crosses the text of "Source"`,
		`slide 2: words are drawn over each other`,
		`slide 2: "Fine print" in Fine print is set at 9 pt`,
		`slide 2: "Faint words on a pale ground" in Faint is #C8C8C8 on #FFFFFF`,
		`slide 2: the picture "Chart" runs off the slide`,
		`slide 2: the text of "Covered" ("Hidden behind the card") is hidden under the shape "Late card"`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no finding %q in:\n%s", want, all)
		}
	}
}
