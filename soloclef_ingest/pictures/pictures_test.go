package pictures

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/internal/testpdf"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
)

// photo is a JPEG of noise, so it does not compress under a kilobyte, and
// seeded, so two photos of different seeds are different pictures.
func photo(t *testing.T, side int, seed uint32) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, side, side))
	for index := range picture.Pix {
		seed = seed*1664525 + 1013904223
		picture.Pix[index] = byte(seed >> 24)
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, picture, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// dot is a JPEG under a kilobyte: decoration.
func dot(t *testing.T) []byte {
	t.Helper()
	picture := image.NewGray(image.Rect(0, 0, 4, 4))
	for index := range picture.Pix {
		picture.Pix[index] = color.Gray{Y: 128}.Y
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, picture, nil); err != nil {
		t.Fatal(err)
	}
	if out.Len() >= minimumBytes {
		t.Fatalf("the dot is %d bytes", out.Len())
	}
	return out.Bytes()
}

// stubVision is a proxy whose vision model calls every picture "a <n>th
// picture", counting the calls, or fails with status when it is not 200.
func stubVision(t *testing.T, status int) (vision.Endpoint, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !strings.Contains(body.Messages[0].Content[0].Text, "Do NOT transcribe text") {
			t.Errorf("an embedded picture was asked with the wrong prompt")
		}
		count := calls.Add(1)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
			"content": "- An axial CT slice, described " + string(rune('0'+count)) + ".",
		}}}})
	}))
	t.Cleanup(server.Close)
	return vision.Endpoint{BaseURL: server.URL + "/v1", Key: "key", Model: "vision-model", HTTP: server.Client(), Timeout: 5 * time.Second}, &calls
}

// parsed is what a pymupdf4llm-like parser makes of the pages: each page's
// text, ending in a blank line, no image markers.
func parsed(t *testing.T, pages ...string) parse.Document {
	t.Helper()
	var builder parse.Builder
	for index, text := range pages {
		builder.Page(index+1, text+"\n\n")
	}
	document, err := builder.Document(parse.Local)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestAPDFsPictureIsDescribedOnItsOwnPage(t *testing.T) {
	scan := photo(t, 64, 1)
	pdf := testpdf.WithPictures([]string{"Patient history", "Imaging", "Plan"},
		testpdf.Picture{Page: 2, JPEG: scan, Width: 64, Height: 64},
		// The same scan again, and a dot: one described, one not worth it.
		testpdf.Picture{Page: 3, JPEG: scan, Width: 64, Height: 64},
		testpdf.Picture{Page: 3, JPEG: dot(t), Width: 4, Height: 4},
	)
	client, calls := stubVision(t, http.StatusOK)

	document, outcome, err := Describe(context.Background(), client, pdf, parsed(t, "Patient history", "Imaging", "Plan"))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Described != 1 || outcome.Duplicate != 1 || outcome.TooSmall != 1 || outcome.Failed != 0 || calls.Load() != 1 {
		t.Fatalf("outcome = %+v after %d calls", outcome, calls.Load())
	}
	// The description is on page 2, after its text, and the page table
	// still says where every page is.
	if len(document.Pages) != 3 {
		t.Fatalf("pages = %+v", document.Pages)
	}
	first, second, third := document.PageMarkdown(0), document.PageMarkdown(1), document.PageMarkdown(2)
	if first != "Patient history\n\n" || third != "Plan\n\n" {
		t.Errorf("the other pages changed: %q, %q", first, third)
	}
	if !strings.HasPrefix(second, "Imaging") || !strings.Contains(second, "**Embedded image:** `Im1` (page 2)") ||
		!strings.Contains(second, "An axial CT slice, described 1.") {
		t.Errorf("page 2 = %q", second)
	}
	if strings.Contains(document.Markdown, "_Note:") {
		t.Errorf("a note with nothing failed: %q", document.Markdown)
	}
}

func TestPicturesTheModelFailedOnAreSaidAndTheDocumentStillIndexes(t *testing.T) {
	pdf := testpdf.WithPictures([]string{"One", "Two"}, testpdf.Picture{Page: 1, JPEG: photo(t, 48, 7), Width: 48, Height: 48})
	client, _ := stubVision(t, http.StatusBadGateway)

	original := parsed(t, "One", "Two")
	document, outcome, err := Describe(context.Background(), client, pdf, original)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Failed != 1 || outcome.Described != 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if !strings.HasPrefix(document.Markdown, original.Markdown) || !strings.Contains(document.Markdown, "_Note: 1 embedded picture(s) could not be described") {
		t.Errorf("document = %q", document.Markdown)
	}
	// The note is after every page, in no page's span.
	if document.Pages[1].End != original.Pages[1].End {
		t.Errorf("the note moved a page: %+v", document.Pages)
	}
}

func TestAPDFThatIsNotOneIsSaidNotFailed(t *testing.T) {
	client, calls := stubVision(t, http.StatusOK)
	document, _, err := Describe(context.Background(), client, []byte("%PDF-1.4 broken"), parsed(t, "Text"))
	if err != nil || !strings.Contains(document.Markdown, "could not be read out of the PDF") || calls.Load() != 0 {
		t.Fatalf("document = %q, %v", document.Markdown, err)
	}
}

func TestTheCallersCancellationIsReturned(t *testing.T) {
	pdf := testpdf.WithPictures([]string{"One"}, testpdf.Picture{Page: 1, JPEG: photo(t, 48, 3), Width: 48, Height: 48})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client, _ := stubVision(t, http.StatusOK)
	if _, _, err := Describe(ctx, client, pdf, parsed(t, "One")); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func described(page int, name, text string) found {
	return found{page: page, name: name, description: text}
}

func TestADescriptionTakesThePlaceOfItsPagesMarker(t *testing.T) {
	var builder parse.Builder
	builder.Separator = "\n\n"
	builder.Page(1, "Intro ![](_page_0_Picture_1.jpeg) after")
	builder.Page(2, "<figure>Bar chart: 3, 5, 8</figure>\nNext\n<!-- image -->\nImage: scan.png\nEnd")
	document, _ := builder.Document(parse.Datalab)

	merged := merge(document, []found{
		described(1, "Im1", "A photo."),
		described(2, "Im2", "A chart."),
		described(2, "Im3", "A scan."),
	})
	first, second := merged.PageMarkdown(0), merged.PageMarkdown(1)
	if strings.Contains(first, "![](") || !strings.HasPrefix(first, "Intro ") || !strings.HasSuffix(first, " after") || !strings.Contains(first, "A photo.") {
		t.Errorf("page 1 = %q", first)
	}
	// The figure is kept and its description follows it; the placeholder
	// and its caption are replaced.
	if !strings.HasPrefix(second, "<figure>Bar chart: 3, 5, 8</figure>") || strings.Index(second, "A chart.") > strings.Index(second, "Next") {
		t.Errorf("page 2 = %q", second)
	}
	if strings.Contains(second, "<!-- image -->") || strings.Contains(second, "Image: scan.png") || !strings.Contains(second, "A scan.") || !strings.HasSuffix(second, "End") {
		t.Errorf("page 2 = %q", second)
	}
	// The separator between the pages is where it was.
	if merged.Markdown[merged.Pages[0].End:merged.Pages[1].Start] != "\n\n" {
		t.Errorf("separator = %q", merged.Markdown[merged.Pages[0].End:merged.Pages[1].Start])
	}
}

func TestADocumentWithNoPagesUsesItsMarkersThenAnAppendedSection(t *testing.T) {
	document, _ := parse.Unpaged(parse.Unstructured, "Top\n<!-- image -->\nBottom")
	merged := merge(document, []found{described(1, "Im1", "First."), described(2, "Im2", "Second.")})
	if strings.Contains(merged.Markdown, "<!-- image -->") || strings.Index(merged.Markdown, "First.") > strings.Index(merged.Markdown, "Bottom") {
		t.Errorf("markdown = %q", merged.Markdown)
	}
	if !strings.Contains(merged.Markdown, "## Image Content (additional, no inline marker found)") || !strings.HasSuffix(strings.TrimSpace(merged.Markdown), "---") {
		t.Errorf("the second picture was not appended: %q", merged.Markdown)
	}
}

func TestAnUploadedPictureIsItsDescription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"A receipt: total 12.40"}}]}`))
	}))
	defer server.Close()
	client := vision.Endpoint{BaseURL: server.URL, Key: "k", Model: "m", HTTP: server.Client(), Timeout: time.Second}
	document, err := Picture(context.Background(), client, "receipt.jpg", photo(t, 16, 2))
	if err != nil || document.Markdown != "A receipt: total 12.40" || document.Parser != parse.Vision || document.Pages != nil {
		t.Fatalf("document = %+v, %v", document, err)
	}
	if !IsPicture("Receipt.JPG") || IsPicture("notes.pdf") || !IsPDF("a.PDF") {
		t.Error("the picture extensions are wrong")
	}
}
