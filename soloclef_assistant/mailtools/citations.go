package mailtools

import (
	"strconv"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/cite"
)

// A message is cited the way a web page is: its result carries a [n] label
// from the turn's registry, and the answer's chip shows who sent it, its
// subject and its day (cite.MailSource), since there is no address to open.
//
// The label is made from the sender, the subject and the day, and from
// nothing that differs between a search's line and a read of the same
// message, so finding a message and then reading it cites it once. (The
// day is the one each result has: a search's is when the server received
// it, a read's the Date header, which are the same day for nearly all
// mail; a message sent just before midnight may be numbered twice, which
// is two chips for one message, never one chip for two.)

// mailLabel is a message as its chip names it.
func mailLabel(from, subject string, date time.Time) string {
	parts := []string{oneLine(from), oneLine(subject)}
	if parts[0] == "" {
		parts[0] = "Unknown sender"
	}
	if parts[1] == "" {
		parts[1] = "(no subject)"
	}
	if !date.IsZero() {
		parts = append(parts, date.UTC().Format("2006-01-02"))
	}
	return strings.Join(parts, " · ")
}

// numbered is the "[n] " a message's result starts with, or "" in a call
// with no registry (a tool run outside a turn, as the tests run some).
func numbered(registry *citation.Registry, from, subject string, date time.Time) string {
	if registry == nil {
		return ""
	}
	return "[" + strconv.Itoa(registry.Number(cite.MailSource(mailLabel(from, subject, date)))) + "] "
}

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }
