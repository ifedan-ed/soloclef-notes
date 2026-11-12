package siteannouncements

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// The limits of a draft, which the form states too.
const (
	titleMax = 120
	bodyMax  = 2000
	linkMax  = 2048
)

// draft is what an administrator may send: a title, a short body, and
// optionally a link and a time after which inboxes stop showing it.
type draft struct {
	Title     string
	Body      string
	Link      *string
	ExpiresAt *time.Time
}

// readDraft reads and checks a request body. Every mistake is reported, by
// field, so the form can show them all at once.
func readDraft(raw []byte, now time.Time) (draft, []errorenvelope.Failure) {
	var failures []errorenvelope.Failure
	refuse := func(field, message string) {
		failures = append(failures, errorenvelope.Failure{Location: []string{"body", field}, Message: message})
	}
	var sent struct {
		Title     *string `json:"title"`
		Body      *string `json:"body"`
		Link      *string `json:"link"`
		ExpiresAt *string `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &sent); err != nil {
		return draft{}, []errorenvelope.Failure{{Location: []string{"body"}, Message: "The request body is not the JSON object an announcement is sent as."}}
	}
	var checked draft

	// Runs of white space in a title are one space: it is a single line
	// in the inbox however it was pasted.
	if sent.Title != nil {
		checked.Title = strings.Join(strings.Fields(*sent.Title), " ")
	}
	switch {
	case checked.Title == "":
		refuse("title", "Give the announcement a title.")
	case utf8.RuneCountInString(checked.Title) > titleMax:
		refuse("title", "Keep the title under 120 characters.")
	}

	if sent.Body != nil {
		checked.Body = strings.TrimSpace(*sent.Body)
	}
	switch {
	case checked.Body == "":
		refuse("body", "Write the announcement.")
	case utf8.RuneCountInString(checked.Body) > bodyMax:
		refuse("body", "Keep the announcement under 2000 characters.")
	}
	// Postgres stores no NUL in text; say so here instead of as a 500.
	if strings.ContainsRune(checked.Title, 0) || strings.ContainsRune(checked.Body, 0) {
		refuse("body", "The announcement contains a character that cannot be stored.")
	}

	if sent.Link != nil {
		if link := strings.TrimSpace(*sent.Link); link != "" {
			if linkAllowed(link) {
				checked.Link = &link
			} else {
				refuse("link", "The link must be a web address or a page in the app.")
			}
		}
	}

	if sent.ExpiresAt != nil && strings.TrimSpace(*sent.ExpiresAt) != "" {
		expires, err := readExpiry(strings.TrimSpace(*sent.ExpiresAt))
		switch {
		case err != nil:
			refuse("expires_at", "The expiry must be a date and time, such as 2026-12-01T09:00:00Z.")
		case !expires.After(now):
			refuse("expires_at", "The expiry must be in the future.")
		default:
			expires = expires.UTC()
			checked.ExpiresAt = &expires
		}
	}
	return checked, failures
}

// linkAllowed is whether a link is a page in the app ("/dashboard/…") or a
// web address, and nothing that runs. Every inbox on the site renders it,
// so "javascript:" and the like must never be stored. "//host" and
// "/\host" are another site to a browser, not a page in this one.
func linkAllowed(link string) bool {
	if utf8.RuneCountInString(link) > linkMax || strings.IndexFunc(link, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return false
	}
	if strings.HasPrefix(link, "/") {
		return len(link) == 1 || (link[1] != '/' && link[1] != '\\')
	}
	lower := strings.ToLower(link)
	return strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://")
}

// readExpiry reads the form's expiry. A time with no offset is UTC.
func readExpiry(text string) (time.Time, error) {
	if moment, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return moment, nil
	}
	if moment, err := time.Parse("2006-01-02T15:04:05.999999999", text); err == nil {
		return moment, nil
	}
	// What an <input type="datetime-local"> holds.
	return time.Parse("2006-01-02T15:04", text)
}
