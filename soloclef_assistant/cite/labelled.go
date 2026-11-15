package cite

import (
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
)

// A source with no address (an email, a connected app's answer) is still
// cited the way a web page is: a [n] label in the tool's result, a chip in
// the answer. Its chip cannot open anything, so what it shows travels in
// the marker itself:
//
//	[citation:src_mail_<label>]   an email in the asker's mailbox
//	[citation:src_app_<label>]    a result from one of their connected apps
//
// <label> is base64url without padding (RFC 4648 §5) of a short UTF-8 line
// such as "Ann Smith <ann@example.org> · Invoice 4411 · 2026-11-03". The
// web and phone apps decode it into the chip's popover
// (soloclef_web/lib/citations/citation-parser.ts, the phone's
// src/lib/render/citations.ts). Encoded, the line cannot hold the "]" or
// line break that would end the marker early, whatever a subject says.
//
// Why carry the label rather than an id the page looks up: there is
// nothing to look it up in. A mailbox is read live and nothing of it is
// stored, and an id would name a message only the asker's own session
// could open. The public pages drop every marker (internal/chat/threads,
// internal/notebooklinks), so the label never reaches a visitor.

// The kinds are Mail and App (cite.go).

// MaxLabelBytes bounds a label before it is encoded: a sender, a subject
// and a date fit, and a marker stays short in the saved answer.
const MaxLabelBytes = 200

// The marker prefixes, one per kind; the apps match exactly these.
const (
	mailPrefix = "src_mail_"
	appPrefix  = "src_app_"
)

// MailSource is the source a mail message is numbered under.
func MailSource(label string) citation.Source {
	return citation.Source{Kind: Mail, Ref: mailPrefix + encodeLabel(label)}
}

// AppSource is the source a connected app's result is numbered under.
func AppSource(label string) citation.Source {
	return citation.Source{Kind: App, Ref: appPrefix + encodeLabel(label)}
}

// encodeLabel is the label on one line, cut to MaxLabelBytes without
// splitting a character, then encoded. A label that is empty after that
// is said as "Untitled": a chip with nothing in its popover reads as a
// fault.
func encodeLabel(label string) string {
	line := strings.Join(strings.Fields(label), " ")
	if len(line) > MaxLabelBytes {
		cut := MaxLabelBytes
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		line = strings.TrimSpace(line[:cut]) + "…"
	}
	if line == "" {
		line = "Untitled"
	}
	return base64.RawURLEncoding.EncodeToString([]byte(line))
}

// LabelOf decodes a labelled ref back to its kind and line: what the apps
// draw, and what a test reads. ok is false for any other ref.
func LabelOf(ref string) (kind, label string, ok bool) {
	var encoded string
	switch {
	case strings.HasPrefix(ref, mailPrefix):
		kind, encoded = Mail, ref[len(mailPrefix):]
	case strings.HasPrefix(ref, appPrefix):
		kind, encoded = App, ref[len(appPrefix):]
	default:
		return "", "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || !utf8.Valid(decoded) {
		return "", "", false
	}
	return kind, string(decoded), true
}
