package indexing

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/pictures/picturestest"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
)

// chunkText is every chunk row's text, joined.
func (h *harness) chunkText() string {
	var all []string
	for _, id := range h.database.ids() {
		all = append(all, h.database.rows[id].content)
	}
	return strings.Join(all, "")
}

func TestAnUploadedPictureWithVisionOnIsIndexedAsItsDescription(t *testing.T) {
	h := newHarness(t)
	h.upload("scan.jpg", string(picturestest.Photo(32, 5)))
	h.database.document.Vision = true
	h.database.document.VisionChoice = "account-vision"
	h.config.VisionModel = "deployment-vision"

	h.mustIndex()
	if !strings.Contains(h.chunkText(), "A grey scan of") {
		t.Errorf("chunks = %q", h.chunkText())
	}
	// The owner's choice, not the deployment's, and the whole picture's
	// prompt: it is the document.
	if !slices.Equal(h.seer.asked, []string{"account-vision"}) || h.seer.purposes[0] != vision.Whole {
		t.Errorf("asked %v for %v", h.seer.asked, h.seer.purposes)
	}
	if h.local.calls != 0 {
		t.Error("the picture was sent to the document parser too")
	}
}

func TestAPDFsPicturesAreDescribedOnTheirPageAndTheFileIsFetchedOnce(t *testing.T) {
	h := newHarness(t)
	h.upload("report.pdf", string(picturestest.PDF([]string{"History", "Imaging", "Plan"}, 2)))
	h.local.pages = []string{"History.\n\n", "Imaging.\n\n", "Plan.\n\n"}
	h.database.document.Vision = true
	h.config.VisionModel = "deployment-vision"

	result := h.mustIndex()
	if h.local.calls != 1 || !slices.Equal(h.seer.asked, []string{"deployment-vision"}) || h.seer.purposes[0] != vision.Embedded {
		t.Fatalf("parsed %d times, asked %v for %v", h.local.calls, h.seer.asked, h.seer.purposes)
	}
	if result.Bytes != h.database.document.File.Size {
		t.Errorf("fetched %d bytes of a %d-byte file: read more than once, or not whole", result.Bytes, h.database.document.File.Size)
	}
	// The chunk that holds the description is on page 2.
	found := false
	for _, id := range h.database.ids() {
		chunk := h.database.rows[id]
		if strings.Contains(chunk.content, "A grey scan of") {
			found = true
			if chunk.place.PageStart == nil || *chunk.place.PageStart != 2 {
				t.Errorf("the description's chunk is on page %v", chunk.place.PageStart)
			}
		}
	}
	if !found {
		t.Errorf("no chunk holds the description: %q", h.chunkText())
	}
}

func TestAPDFWhosePicturesFailedStillIndexesAndSaysSo(t *testing.T) {
	h := newHarness(t)
	h.upload("report.pdf", string(picturestest.PDF([]string{"One", "Two"}, 1)))
	h.local.pages = []string{"One.\n\n", "Two.\n\n"}
	h.database.document.Vision = true
	h.database.document.VisionChoice = "account-vision"
	h.seer.err = &parse.Error{Kind: parse.Unavailable, Parser: parse.Vision, Message: "the proxy did not answer"}

	h.mustIndex()
	if !strings.Contains(h.chunkText(), "could not be described by the vision model") {
		t.Errorf("chunks = %q", h.chunkText())
	}
}

func TestVisionAskedForWithNoModelFailsBeforeAnythingIsRead(t *testing.T) {
	h := newHarness(t)
	h.upload("report.pdf", "%PDF-1.4")
	h.database.document.Vision = true
	result, err := h.run()
	if err != nil || result.Outcome != Failed || !strings.Contains(result.Reason, "no vision model is set") {
		t.Fatalf("outcome = %s (%s), %v", result.Outcome, result.Reason, err)
	}
	if h.local.calls != 0 || len(h.seer.opened) != 0 || result.Bytes != 0 {
		t.Error("the file was read although its pictures could not be described")
	}
}

// An owner who chose a model of their own connection has their pictures
// described by it, opened for them.
func TestAnOwnersOwnVisionModelDescribesTheirPictures(t *testing.T) {
	h := newHarness(t)
	h.upload("scan.jpg", string(picturestest.Photo(32, 5)))
	h.database.document.Vision = true
	h.database.document.VisionChoice = "own:42"
	h.config.VisionModel = "deployment-vision"
	h.mustIndex()
	if !slices.Equal(h.seer.opened, []string{testOwner + "=own:42"}) || !slices.Equal(h.seer.asked, []string{"own:42"}) {
		t.Fatalf("opened %v, asked %v", h.seer.opened, h.seer.asked)
	}
}

// A chosen own model that cannot be called fails the document in its own
// sentence, before anything is read, and is not tried again unchanged;
// the deployment's model is not used in its place. A failure to open it
// that may pass hands the document back.
func TestAnOwnVisionModelThatCannotBeOpenedIsSaidSo(t *testing.T) {
	h := newHarness(t)
	h.upload("report.pdf", "%PDF-1.4")
	h.database.document.Vision = true
	h.database.document.VisionChoice = "own:42"
	h.config.VisionModel = "deployment-vision"
	h.seer.openErr = &ownmodels.Unavailable{Message: "The model you chose, Lab vision, is switched off in your model connections. Switch it on, or choose another model."}
	result, err := h.run()
	if err != nil || result.Outcome != Failed || !strings.Contains(result.Reason, "is switched off") || !strings.Contains(result.Reason, "index the document again") {
		t.Fatalf("outcome = %s (%s), %v", result.Outcome, result.Reason, err)
	}
	if h.local.calls != 0 || len(h.seer.asked) != 0 || result.Bytes != 0 || slices.Contains(h.seer.opened, testOwner+"=deployment-vision") {
		t.Errorf("read %d times, asked %v, opened %v", h.local.calls, h.seer.asked, h.seer.opened)
	}

	h = newHarness(t)
	h.upload("report.pdf", "%PDF-1.4")
	h.database.document.Vision = true
	h.database.document.VisionChoice = "own:42"
	h.seer.openErr = errors.New("the database is away")
	if result, err := h.run(); err == nil || result.Outcome != Retry {
		t.Fatalf("outcome = %s, %v", result.Outcome, err)
	}
}

func TestAPictureTheVisionModelIsDownForIsHandedBack(t *testing.T) {
	h := newHarness(t)
	h.upload("scan.png", string(picturestest.Photo(16, 1)))
	h.database.document.Vision = true
	h.config.VisionModel = "deployment-vision"
	h.seer.err = &parse.Error{Kind: parse.Unavailable, Parser: parse.Vision, Message: "the proxy did not answer"}
	if result, err := h.run(); err == nil || result.Outcome != Retry {
		t.Fatalf("outcome = %s, %v", result.Outcome, err)
	}
}

func TestWithVisionOffAPDFIsParsedAsAlways(t *testing.T) {
	h := newHarness(t)
	h.upload("report.pdf", string(picturestest.PDF([]string{"One"}, 1)))
	h.local.pages = []string{"One.\n\n"}
	h.mustIndex()
	if len(h.seer.asked) != 0 || strings.Contains(h.chunkText(), "Embedded image") {
		t.Errorf("vision was used although the upload did not ask: %v", h.seer.asked)
	}
}
