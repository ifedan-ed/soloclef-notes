package hostedapi

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

// mostServiceWords bounds the service's own words in a person's failure
// reason: one sentence or two, never a page.
const mostServiceWords = 240

// errorClassPrefix is the "litellm.BadRequestError: " or "OpenAIException - "
// a proxy or an SDK puts before the provider's own sentence. It names a
// class in someone's code, which says nothing to the person reading why a
// source failed.
var errorClassPrefix = regexp.MustCompile(`^[A-Za-z_][\w.]*(Error|Exception)\s*[:-]\s*`)

// serviceWords is the sentence a service put in an error reply, or "" when
// it put none a person could read. APIs disagree on where it goes, so the
// common places are tried in turn: {"error": {"message"}}, {"error": "..."},
// {"detail": "..."} and {"message": "..."}. A reply that is not JSON counts
// only when it is one short line of plain text (a proxy's "Bad Gateway"),
// never an HTML page. Whatever was found is cut to a sentence's length; the
// whole reply goes to the log through parse.Error.Err instead.
func serviceWords(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	var text string
	if strings.HasPrefix(body, "{") {
		text = jsonMessage(body)
	} else if !strings.HasPrefix(body, "<") && !strings.Contains(body, "\n") {
		text = body
	}
	return tidyWords(text)
}

// jsonMessage reads the message out of an error reply's usual places.
func jsonMessage(body string) string {
	var reply map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &reply) != nil {
		return ""
	}
	if raw, ok := reply["error"]; ok {
		var nested struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &nested) == nil && nested.Message != "" {
			return nested.Message
		}
		var flat string
		if json.Unmarshal(raw, &flat) == nil && flat != "" {
			return flat
		}
	}
	for _, field := range []string{"detail", "message"} {
		var flat string
		if json.Unmarshal(reply[field], &flat) == nil && flat != "" {
			return flat
		}
	}
	return ""
}

// tidyWords strips the class names before the sentence, keeps its first
// line, drops it if what is left is more JSON (a provider's reply quoted
// inside the proxy's), and cuts it at a word near mostServiceWords.
func tidyWords(text string) string {
	text = strings.TrimSpace(text)
	for {
		stripped := errorClassPrefix.ReplaceAllString(text, "")
		if stripped == text {
			break
		}
		text = strings.TrimSpace(stripped)
	}
	if line, _, found := strings.Cut(text, "\n"); found {
		text = strings.TrimSpace(line)
	}
	if strings.ContainsAny(text, "{}") {
		return ""
	}
	if utf8.RuneCountInString(text) > mostServiceWords {
		cut := string([]rune(text)[:mostServiceWords])
		if space := strings.LastIndex(cut, " "); space > mostServiceWords/2 {
			cut = cut[:space]
		}
		text = strings.TrimRight(cut, " ,;:.") + "…"
	}
	return strings.TrimRight(text, ". ")
}
