package deliverables

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"
)

// previewDeck has LibreOffice draw a deck in the thread's sandbox: the PDF
// the viewer shows, and the list of every drawn word and its box that the
// layout rules read (layout_rules.go). It is one command, the one the
// built documents' verification runs, with no code of the model's in it:
// the sandbox is used here only because it is where LibreOffice is.
//
// It answers the preview and the rules' findings. An error is the sandbox
// failing, worded for the model (try again, or report it); a finding is a
// fault in the drawing, recorded with the deck.
func (s Specialist) previewDeck(ctx context.Context, owner Owner, data []byte, slides int) ([]byte, []string, error) {
	bench, err := s.bench(ctx, owner)
	if err != nil {
		return nil, nil, err
	}
	build := "/tmp/soloclef-deck-" + s.Saver.NewID()
	source := build + ".pptx"
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		_, _ = bench.Run(cleanup, "rm -rf -- "+quoted(build)+" "+quoted(source))
	}()
	if err := bench.WriteFile(ctx, source, data); err != nil {
		return nil, nil, s.sandboxFailure(ctx, owner, "drawing the deck's preview", err)
	}
	format := documentFormats[FormatPPTX]
	ran, err := bench.Run(ctx, renderCommand(build, source, format))
	if err != nil {
		return nil, nil, s.sandboxFailure(ctx, owner, "drawing the deck's preview", err)
	}
	rendered := readRendering(ran.Output)
	if ran.TimedOut || rendered.failed != "" || !ran.OK() || rendered.pages < 1 {
		// LibreOffice could not open a file this process wrote and read
		// back: a fault in the drawing or the sandbox, not the model's.
		s.Saver.Logger.ErrorContext(ctx, "LibreOffice could not draw a deck's preview",
			"step", rendered.failed, "timed_out", ran.TimedOut, "output", clip(ran.Output), "user_id", owner.UserID)
		s.Failures.note(FailurePreview, fmt.Errorf("LibreOffice could not draw the preview (step %q, timed out %v)", rendered.failed, ran.TimedOut))
		return nil, nil, errors.New("the deck's preview could not be drawn; try once more, and if it fails again stop and report it")
	}
	findings := renderFindings(rendered, format, slides)
	listed, err := bench.ReadFile(ctx, build+"/"+wordsFile, maxWordsBytes)
	switch {
	case err != nil && !errors.Is(err, sandbox.ErrNotFound) && !errors.Is(err, sandbox.ErrTooLarge):
		return nil, nil, s.sandboxFailure(ctx, owner, "reading the deck's preview", err)
	case err != nil:
		findings = append(findings, "the positions of the drawn text could not be read from the preview")
	default:
		pages, readErr := readPDFWords(listed)
		shapes, shapesErr := readDeckShapes(data)
		switch {
		case readErr != nil || len(pages) != rendered.pages:
			findings = append(findings, "the positions of the drawn text could not be read from the preview")
		case shapesErr != nil:
			findings = append(findings, "the deck's shapes could not be read back: "+shapesErr.Error())
		default:
			findings = append(findings, layoutFindings(pages, &shapes)...)
		}
	}
	preview, err := bench.ReadFile(ctx, build+"/primary.pdf", MaxDocumentBytes)
	if err != nil {
		if errors.Is(err, sandbox.ErrTooLarge) {
			return nil, nil, fmt.Errorf("the deck's preview is larger than %d bytes; make the deck shorter", MaxDocumentBytes)
		}
		return nil, nil, s.sandboxFailure(ctx, owner, "reading the deck's preview", err)
	}
	return preview, findings, nil
}
