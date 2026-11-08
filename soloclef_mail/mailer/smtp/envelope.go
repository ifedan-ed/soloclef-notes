package smtp

import "strings"

// envelopeAddress is the address out of a header value, as smtplib's
// send_message takes it: it hands MAIL FROM and RCPT TO what
// email.utils.getaddresses found, not the header it came from.
//
// Without this, MAIL FROM carries the display name too -- MAIL
// FROM:<Soloclef Notes <notes@example.test>> -- and the server refuses the
// sender. Nothing in Go complained: the refusal came back as a 5xx and would
// have been logged as our From domain being unverified.
//
// What it does not do is parse an address list. Every value here is one
// address, in one of the two shapes the three settings and a user row produce:
// "a@b" or "Name <a@b>", with the name quoted or not. A list would need
// email.utils.getaddresses' own quoting and comment rules, and no caller sends
// one.
func envelopeAddress(header string) string {
	if start := strings.LastIndex(header, "<"); start >= 0 {
		if end := strings.Index(header[start:], ">"); end >= 0 {
			return strings.TrimSpace(header[start+1 : start+end])
		}
	}
	return strings.TrimSpace(header)
}
