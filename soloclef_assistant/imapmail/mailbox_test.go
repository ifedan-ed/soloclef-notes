package imapmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// held is one message the test server holds.
type held struct {
	uid  uint32
	seen bool
	// received is the INTERNALDATE as the server words it.
	received string
	// raw is the message's bytes; unsent is how much longer the server
	// says the message is than the bytes it has, which is how a message
	// larger than the client reads is stood in for.
	raw    string
	unsent int
}

// criteriaWords splits a search's criteria into its words, a quoted string being
// one word.
func criteriaWords(criteria string) []string {
	var out []string
	for rest := strings.TrimSpace(criteria); rest != ""; rest = strings.TrimSpace(rest) {
		if rest[0] != '"' {
			word, after, _ := strings.Cut(rest, " ")
			out, rest = append(out, word), after
			continue
		}
		quoted, err := strconv.QuotedPrefix(rest)
		if err != nil {
			return append(out, rest)
		}
		value, _ := strconv.Unquote(quoted)
		out, rest = append(out, value), rest[len(quoted):]
	}
	return out
}

// matches applies a search's criteria the way a mail server does, for the
// keys this client sends.
func (h held) matches(criteria []string) bool {
	lowered := strings.ToLower(h.raw)
	header := func(name string) string {
		for _, line := range strings.Split(h.raw[:strings.Index(h.raw, "\r\n\r\n")], "\r\n") {
			if value, found := strings.CutPrefix(strings.ToLower(line), name+": "); found {
				return value
			}
		}
		return ""
	}
	received, _ := time.Parse("_2-Jan-2006 15:04:05 -0700", h.received)
	for index := 0; index < len(criteria); index++ {
		value := func() string { index++; return strings.ToLower(criteria[index]) }
		switch criteria[index] {
		case "ALL":
		case "CHARSET":
			value()
		case "TEXT":
			if !strings.Contains(lowered, value()) {
				return false
			}
		case "FROM", "TO", "SUBJECT":
			if name := strings.ToLower(criteria[index]); !strings.Contains(header(name), value()) {
				return false
			}
		case "UNSEEN":
			if h.seen {
				return false
			}
		case "SINCE", "BEFORE":
			since := criteria[index] == "SINCE"
			day, err := time.Parse("2-Jan-2006", criteria[index+1])
			index++
			if err != nil || since == received.Before(day) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func message(headers []string, body string) string {
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + body
}

// The mailbox every test below reads.
func stocked(t *testing.T) (*mailServer, IMAP, Settings) {
	t.Helper()
	server, pool := newMailServer(t, true)
	invoice := message([]string{
		"From: =?UTF-8?B?QsO8Y2hlcmVp?= <books@example.org>",
		"To: ada@example.com",
		"Subject: =?ISO-8859-1?Q?Rechnung_f=FCr_M=E4rz?=",
		"Date: Tue, 03 Mar 2026 09:15:00 +0100",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="outer"`,
	}, strings.Join([]string{
		"--outer",
		`Content-Type: multipart/alternative; boundary="inner"`,
		"",
		"--inner",
		"Content-Type: text/plain; charset=ISO-8859-1",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"Guten Tag, anbei die Rechnung =FCber 42 Euro.",
		"--inner",
		"Content-Type: text/html; charset=UTF-8",
		"",
		"<p>Guten Tag</p>",
		"--inner--",
		"--outer",
		`Content-Type: text/csv; name="posten.csv"`,
		`Content-Disposition: attachment; filename="posten.csv"`,
		"Content-Transfer-Encoding: base64",
		"",
		"UG9zdGVuLEJldHJhZwpCdWNoLDQyCg==",
		"--outer",
		`Content-Type: application/pdf; name="rechnung.pdf"`,
		`Content-Disposition: attachment; filename="rechnung.pdf"`,
		"Content-Transfer-Encoding: base64",
		"",
		"JVBERi0xLjQK",
		"--outer--",
		"",
	}, "\r\n"))
	newsletter := message([]string{
		"From: News <news@example.net>",
		"To: ada@example.com",
		"Subject: This week",
		"Content-Type: text/html; charset=UTF-8",
	}, `<h1>Headlines</h1><p>Read <a href="https://example.net/a">the first story</a>.</p>`)
	server.folders = map[string][]held{
		"INBOX": {
			{uid: 4, seen: true, received: " 3-Mar-2026 08:15:02 +0000", raw: invoice},
			{uid: 9, received: "10-Mar-2026 07:00:00 +0000", raw: newsletter},
			{uid: 12, received: "11-Mar-2026 07:00:00 +0000", raw: message([]string{"From: carol@example.org", "To: ada@example.com", "Subject: Lunch \"soon\"?"}, "Tomorrow at noon."), unsent: 9 << 20},
		},
		// "Entwürfe" as the server names it.
		"Entw&APw-rfe": {{uid: 1, received: " 1-Mar-2026 07:00:00 +0000", raw: message([]string{"Subject: Draft"}, "unfinished")}},
	}
	var dialled []string
	mail := client(server, pool, map[string][]string{"example.com": {"93.184.216.34"}}, &dialled)
	return server, mail, Settings{Host: "example.com", Port: 993, UseTLS: true, Username: "ada@example.com", Password: `fake-pass "quoted" \ word`}
}

func TestSearchingAFolder(t *testing.T) {
	server, mail, settings := stocked(t)
	ctx := context.Background()

	everything, more, err := mail.Search(ctx, settings, Query{Limit: 10})
	if err != nil || more || len(everything) != 3 {
		t.Fatalf("an unfiltered search: %d messages, more %v, %v", len(everything), more, err)
	}
	// Newest first, with encoded headers decoded and the read state told.
	if got := []uint32{everything[0].UID, everything[1].UID, everything[2].UID}; got[0] != 12 || got[1] != 9 || got[2] != 4 {
		t.Errorf("the order is %v; want 12, 9, 4", got)
	}
	oldest := everything[2]
	if oldest.From != "Bücherei <books@example.org>" || oldest.Subject != "Rechnung für März" || oldest.Unread {
		t.Errorf("the oldest message reads %+v", oldest)
	}
	if !oldest.Date.Equal(time.Date(2026, 3, 3, 8, 15, 2, 0, time.UTC)) || !everything[0].Unread {
		t.Errorf("date %v, newest unread %v", oldest.Date, everything[0].Unread)
	}

	cases := []struct {
		name  string
		query Query
		want  []uint32
		more  bool
	}{
		{"by sender", Query{From: "example.org", Limit: 10}, []uint32{12, 4}, false},
		{"by subject, a quote and all", Query{Subject: `"soon"`, Limit: 10}, []uint32{12}, false},
		{"unread only", Query{UnreadOnly: true, Limit: 10}, []uint32{12, 9}, false},
		{"since a day", Query{Since: time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC), Limit: 10}, []uint32{12, 9}, false},
		{"before a day", Query{Before: time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC), Limit: 10}, []uint32{4}, false},
		{"every field must match", Query{From: "example.org", UnreadOnly: true, Limit: 10}, []uint32{12}, false},
		{"text outside ASCII goes as a literal", Query{Text: "über", Limit: 10}, nil, false},
		{"a list cut at the limit says so", Query{Limit: 2}, []uint32{12, 9}, true},
		{"another folder, named as a person reads it", Query{Folder: "Entwürfe", Limit: 10}, []uint32{1}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			found, more, err := mail.Search(ctx, settings, c.query)
			if err != nil {
				t.Fatal(err)
			}
			var uids []uint32
			for _, summary := range found {
				uids = append(uids, summary.UID)
			}
			if len(uids) != len(c.want) || more != c.more {
				t.Fatalf("found %v, more %v; want %v, more %v", uids, more, c.want, c.more)
			}
			for index := range uids {
				if uids[index] != c.want[index] {
					t.Fatalf("found %v; want %v", uids, c.want)
				}
			}
		})
	}

	var refused *ConnectionError
	if _, _, err := mail.Search(ctx, settings, Query{Folder: "Nowhere", Limit: 5}); !errors.As(err, &refused) || !strings.Contains(refused.Message, `"Nowhere"`) {
		t.Errorf("a folder that is not there: %v", err)
	}

	// The client only ever reads: every folder is opened read-only, and
	// nothing that could change a mailbox was sent in any of the above.
	for _, line := range server.sent() {
		_, command, _ := strings.Cut(line, " ")
		verb, _, _ := strings.Cut(command, " ")
		switch verb {
		case "LOGIN", "EXAMINE", "UID", "LOGOUT":
		default:
			t.Errorf("sent %q", line)
		}
		if strings.HasPrefix(command, "UID ") && !strings.HasPrefix(command, "UID SEARCH ") && !strings.HasPrefix(command, "UID FETCH ") {
			t.Errorf("sent %q", line)
		}
		if strings.Contains(command, "BODY[") {
			t.Errorf("a fetch that marks a message read was sent: %q", line)
		}
	}
	if sent := strings.Join(server.sent(), "\n"); !strings.Contains(sent, `CHARSET UTF-8 TEXT "über"`) {
		t.Errorf("the non-ASCII search was not sent as UTF-8:\n%s", sent)
	}
}

func TestReadingAMessage(t *testing.T) {
	_, mail, settings := stocked(t)
	ctx := context.Background()

	invoice, err := mail.Read(ctx, settings, "", 4)
	if err != nil {
		t.Fatal(err)
	}
	if invoice.From != "Bücherei <books@example.org>" || invoice.Subject != "Rechnung für März" {
		t.Errorf("headers: %+v", invoice)
	}
	if !invoice.Date.Equal(time.Date(2026, 3, 3, 8, 15, 0, 0, time.UTC)) {
		t.Errorf("date %v", invoice.Date)
	}
	// The plain part wins over the HTML one, in its own charset.
	if invoice.Body != "Guten Tag, anbei die Rechnung über 42 Euro." || invoice.Partial {
		t.Errorf("body %q, partial %v", invoice.Body, invoice.Partial)
	}
	if len(invoice.Attachments) != 2 {
		t.Fatalf("attachments: %+v", invoice.Attachments)
	}
	sheet, document := invoice.Attachments[0], invoice.Attachments[1]
	if sheet.Filename != "posten.csv" || sheet.Text != "Posten,Betrag\nBuch,42\n" || sheet.Bytes != 22 {
		t.Errorf("the text attachment: %+v", sheet)
	}
	// A binary attachment is listed and its bytes are not kept as text.
	if document.Filename != "rechnung.pdf" || document.ContentType != "application/pdf" || document.Text != "" || document.Bytes != 9 {
		t.Errorf("the binary attachment: %+v", document)
	}

	// A message with no plain part is read from its HTML, links kept.
	newsletter, err := mail.Read(ctx, settings, "INBOX", 9)
	if err != nil || !strings.Contains(newsletter.Body, "# Headlines") || !strings.Contains(newsletter.Body, "[the first story](https://example.net/a)") {
		t.Errorf("an HTML-only message: %q, %v", newsletter.Body, err)
	}

	// A message larger than is read says so.
	large, err := mail.Read(ctx, settings, "INBOX", 12)
	if err != nil || large.Body != "Tomorrow at noon." || !large.Partial {
		t.Errorf("a large message: %+v, %v", large, err)
	}

	var refused *ConnectionError
	if _, err := mail.Read(ctx, settings, "INBOX", 77); !errors.As(err, &refused) || !strings.Contains(refused.Message, "not in the folder") {
		t.Errorf("a message that is not there: %v", err)
	}
}

func TestFolderNames(t *testing.T) {
	_, mail, settings := stocked(t)
	names, err := mail.FolderNames(context.Background(), settings)
	if err != nil || strings.Join(names, "|") != "INBOX|Sent|Plans/2026" {
		t.Errorf("FolderNames = %q, %v", names, err)
	}
	for wire, name := range map[string]string{
		"INBOX": "INBOX", "Entw&APw-rfe": "Entwürfe", "R&AOk-sum&AOk-s &- notes": "Résumés & notes",
		"&ZeVnLIqe-": "日本語", "&2D3eAA-": "😀",
	} {
		if got := decodeFolder(wire); got != name {
			t.Errorf("decodeFolder(%q) = %q; want %q", wire, got, name)
		}
		if got := encodeFolder(name); got != wire {
			t.Errorf("encodeFolder(%q) = %q; want %q", name, got, wire)
		}
	}
	// A name that is not valid modified UTF-7 is kept as it came.
	if got := decodeFolder("Odd&name"); got != "Odd&name" {
		t.Errorf("an invalid name decoded to %q", got)
	}
}

// A server that announces more than the client will hold ends the
// conversation instead of being read into memory.
func TestALiteralLargerThanItsBudgetIsNotRead(t *testing.T) {
	server, mail, settings := stocked(t)
	server.folders["INBOX"] = []held{{uid: 1, received: " 1-Mar-2026 07:00:00 +0000",
		raw: message([]string{"Subject: " + strings.Repeat("x", headerBudget)}, "body")}}
	var refused *ConnectionError
	if _, _, err := mail.Search(context.Background(), settings, Query{Limit: 5}); !errors.As(err, &refused) || refused != errBroken {
		t.Errorf("an oversized header block: %v", err)
	}
}

func TestAnAttachmentAskedForIsHeldWholeAndOnlyThatOne(t *testing.T) {
	server, mail, settings := stocked(t)
	ctx := context.Background()
	// A message of about 6 MB: past what Read fetches, inside what
	// ReadAttachment does. Its attachment is 4.5 MB of bytes.
	payload := bytes.Repeat([]byte("%PDF-1.4 a scanned contract "), 4_500_000/28)
	encoded := base64.StdEncoding.EncodeToString(payload)
	var lines []string
	for len(encoded) > 76 {
		lines, encoded = append(lines, encoded[:76]), encoded[76:]
	}
	lines = append(lines, encoded)
	raw := message([]string{"From: legal@example.org", "Subject: Contract", "MIME-Version: 1.0", `Content-Type: multipart/mixed; boundary="b"`},
		strings.Join(append(append([]string{"--b", "Content-Type: text/plain", "", "Signed copy attached.",
			"--b", `Content-Type: image/png; name="logo.png"`, `Content-Disposition: attachment; filename="logo.png"`, "Content-Transfer-Encoding: base64", "", "iVBORw0KGgo=",
			"--b", `Content-Type: application/pdf; name="Contract.pdf"`, `Content-Disposition: attachment; filename="Contract.pdf"`, "Content-Transfer-Encoding: base64", ""},
			lines...), "--b--", ""), "\r\n"))
	server.folders["INBOX"] = append(server.folders["INBOX"], held{uid: 20, received: "12-Mar-2026 07:00:00 +0000", raw: raw})

	// Read stops at its limit: the attachment is listed, its bytes are not.
	partial, err := mail.Read(ctx, settings, "INBOX", 20)
	if err != nil || !partial.Partial || partial.Body != "Signed copy attached." {
		t.Fatalf("Read: partial %v, body %q, %v", partial.Partial, partial.Body, err)
	}

	// ReadAttachment fetches the whole message and keeps the one asked for,
	// whatever the case of its name.
	whole, err := mail.ReadAttachment(ctx, settings, "INBOX", 20, "contract.PDF")
	if err != nil || whole.Partial {
		t.Fatalf("ReadAttachment: partial %v, %v", whole.Partial, err)
	}
	if len(whole.Attachments) != 2 {
		t.Fatalf("attachments: %+v", whole.Attachments)
	}
	logo, contract := whole.Attachments[0], whole.Attachments[1]
	if logo.Data != nil {
		t.Errorf("an attachment not asked for was held: %d bytes", len(logo.Data))
	}
	if !bytes.Equal(contract.Data, payload) || contract.Bytes != len(payload) || contract.Text != "" {
		t.Errorf("the attachment asked for: %d bytes held of %d, Bytes %d", len(contract.Data), len(payload), contract.Bytes)
	}
	// And the fetch asked for no more than its own limit.
	if sent := strings.Join(server.sent(), "\n"); !strings.Contains(sent, "BODY.PEEK[]<0."+strconv.Itoa(mostWholeMessageBytes)+">") {
		t.Errorf("the whole fetch was not bounded:\n%s", sent)
	}
}

func TestAnAttachmentOverTheLimitIsHeldOnlyToOneByteMore(t *testing.T) {
	big := bytes.Repeat([]byte{'x'}, MostAttachmentBytes+1000)
	raw := "Subject: Big\r\nContent-Type: multipart/mixed; boundary=\"b\"\r\n\r\n--b\r\nContent-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"dump.bin\"\r\n\r\n" + string(big) + "\r\n--b--\r\n"
	message := parse([]byte(raw), "dump.bin")
	if len(message.Attachments) != 1 || len(message.Attachments[0].Data) != MostAttachmentBytes+1 || message.Attachments[0].Bytes < MostAttachmentBytes+1000 {
		t.Fatalf("held %d bytes, counted %d", len(message.Attachments[0].Data), message.Attachments[0].Bytes)
	}
}
