package pagereader

import (
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
)

// previewChars is how much of a page's text a card shows, and the most of
// it that is saved with the message.
const previewChars = 500

// CardPayload is what the browser is sent when a call ends: the shape the
// page reader's card was written for (Python's web_scrape emission). The
// fields are the whole list. A card is not only shown: it is saved in the
// message, synced to every open page of the thread, and published if the
// thread is shared, so a page's full text must never be in it.
type CardPayload struct {
	// Status is "completed": the call ran. How each page fared is in Pages.
	Status string     `json:"status"`
	Pages  []CardPage `json:"pages"`
	// Succeeded is how many pages were read, Total how many were asked for.
	Succeeded int `json:"succeeded"`
	Total     int `json:"total"`
}

// CardPage is one page on the card.
type CardPage struct {
	URL string `json:"url"`
	// Status is a Status: "success", "empty" or "failed".
	Status string `json:"status"`
	// ContentPreview is at most the first 500 characters of the page's
	// text, with an ellipsis when there was more.
	ContentPreview string `json:"content_preview,omitempty"`
	// Error is why a page has no text, in the reader's own sentence. It is
	// never text from the page or from a server.
	Error string `json:"error,omitempty"`
}

// Card returns what the page reader's card may show of a finished call. ok
// is false for any other tool's result, for a call that failed as a whole,
// and for a result it cannot read back; the caller then shows its default
// card, which shows nothing of the output.
//
// It reads the result text the model was given rather than receiving the
// pages by a side channel such as tool.Call.Notify. A notice is a separate
// event that arrives before the result and is not stored with it, so a
// card built from one would need the chat to hold state per call and
// would have nothing to build from when a saved turn is replayed. The
// result text is the one thing every path has. To make reading it back
// exact, render frames each page with its length in bytes: the page's
// text is skipped by count, never searched, so nothing a page says can
// pose as the next page's header.
func Card(result event.Event) (payload any, ok bool) {
	if result.Tool != ToolName || result.IsError {
		return nil, false
	}
	// "Read 2 of 3 pages." and then the pages.
	_, rest, found := strings.Cut(result.Text, "\n\n")
	if !found {
		return nil, false
	}
	card := CardPayload{Status: "completed"}
	for {
		line, after, _ := strings.Cut(rest, "\n")
		page, size, ok := parseHeader(line)
		if !ok {
			return nil, false
		}
		// A result shortened on its way here ends inside a page. What is
		// left of that page is still its beginning.
		size = min(size, len(after))
		body := after[:size]
		switch Status(page.Status) {
		case Read:
			// The text starts after the first empty line; see render.
			if _, text, found := strings.Cut("\n"+body, "\n\n"); found {
				body = text
			}
			page.ContentPreview = preview(body)
			card.Succeeded++
		case Empty, Failed:
			page.Error = preview(body)
		default:
			return nil, false
		}
		card.Pages = append(card.Pages, page)
		rest = after[size:]
		if rest == "" {
			break
		}
		if !strings.HasPrefix(rest, "\n\n") {
			return nil, false
		}
		rest = rest[2:]
	}
	card.Total = len(card.Pages)
	return card, true
}

// parseHeader reads one header line written by render:
//
//	=== [4] https://example.org/a | success | 1234 bytes ===
//
// It is read from the right, where the fields are a number and one of
// three words, so whatever the address holds cannot shift them.
func parseHeader(line string) (page CardPage, size int, ok bool) {
	line, ok = strings.CutPrefix(line, headerOpen)
	if !ok {
		return page, 0, false
	}
	line, ok = strings.CutSuffix(line, headerClose)
	if !ok {
		return page, 0, false
	}
	at := strings.LastIndex(line, headerField)
	if at < 0 {
		return page, 0, false
	}
	size, err := strconv.Atoi(line[at+len(headerField):])
	if err != nil || size < 0 {
		return page, 0, false
	}
	line = line[:at]
	if at = strings.LastIndex(line, headerField); at < 0 {
		return page, 0, false
	}
	page.URL, page.Status = line[:at], line[at+len(headerField):]
	if Status(page.Status) == Read {
		// A page that was read carries its citation label first. The
		// card has no use for it.
		if _, address, labelled := strings.Cut(page.URL, "] "); labelled && strings.HasPrefix(page.URL, "[") {
			page.URL = address
		}
	}
	return page, size, true
}

// preview is the first 500 characters of s, cut between characters.
func preview(s string) string {
	cut := firstRunes(s, previewChars)
	if len(cut) < len(s) {
		return cut + "…"
	}
	return s
}
