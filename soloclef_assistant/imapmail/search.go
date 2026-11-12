package imapmail

import (
	"bytes"
	"context"
	"mime"
	"net/mail"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html/charset"
)

// MostResults is the most messages one search returns.
const MostResults = 50

// Inbox is the folder a search or a read is about when none is named.
const Inbox = "INBOX"

// Query is what a search looks for. Every field that is set must match.
type Query struct {
	// Folder is the folder searched; "" is the inbox.
	Folder string
	// Text matches anywhere in a message; From, To and Subject in that
	// header.
	Text, From, To, Subject string
	// Since and Before bound the message's date, by day; zero is unbounded.
	Since, Before time.Time
	// UnreadOnly leaves out messages already read.
	UnreadOnly bool
	// Limit is how many messages to return, newest first: 1 to MostResults.
	Limit int
}

// Summary is one message as a search lists it.
type Summary struct {
	// UID names the message within its folder, for Read.
	UID     uint32
	From    string
	To      string
	Subject string
	// Date is when the server received the message.
	Date   time.Time
	Unread bool
}

// headerBudget bounds the four headers fetched per message, and
// summaryBudget every response of one fetch.
const headerBudget = 64 << 10

// examine opens a folder read-only. The server refuses a write to a
// folder opened this way, whatever is sent afterwards.
func examine(talk *session, folder string) error {
	if folder == "" {
		folder = Inbox
	}
	if ok, reason := talk.command(0, nil, "EXAMINE", astring(encodeFolder(folder))); !ok {
		if reason == "" {
			return errBroken
		}
		return &ConnectionError{Message: "The folder " + strconv.Quote(folder) + " could not be opened: " + reason}
	}
	return nil
}

// imapDate is a day as a search criterion takes it.
func imapDate(day time.Time) string { return day.Format("2-Jan-2006") }

// criteria is the query as the pieces of a UID SEARCH command.
func criteria(query Query) []any {
	parts := []any{"UID SEARCH"}
	texts := []struct{ key, value string }{
		{"TEXT", query.Text}, {"FROM", query.From}, {"TO", query.To}, {"SUBJECT", query.Subject},
	}
	for _, text := range texts {
		if _, sentAsLiteral := astring(text.value).(literal); sentAsLiteral {
			// A value outside ASCII is only searchable when the server is
			// told how it is encoded.
			parts = append(parts, "CHARSET UTF-8")
			break
		}
	}
	named := false
	for _, text := range texts {
		if text.value != "" {
			parts = append(parts, text.key, astring(text.value))
			named = true
		}
	}
	if !query.Since.IsZero() {
		parts, named = append(parts, "SINCE "+imapDate(query.Since)), true
	}
	if !query.Before.IsZero() {
		parts, named = append(parts, "BEFORE "+imapDate(query.Before)), true
	}
	if query.UnreadOnly {
		parts, named = append(parts, "UNSEEN"), true
	}
	if !named {
		parts = append(parts, "ALL")
	}
	return parts
}

var (
	fetchUID      = regexp.MustCompile(`\bUID (\d+)`)
	fetchFlags    = regexp.MustCompile(`\bFLAGS \(([^)]*)\)`)
	fetchReceived = regexp.MustCompile(`\bINTERNALDATE "([^"]*)"`)
	fetchSize     = regexp.MustCompile(`\bRFC822\.SIZE (\d+)`)
)

// Search lists the newest messages of a folder that match the query. more
// says the folder holds further matches than were returned, so a caller
// never presents a cut-off list as the whole.
func (i IMAP) Search(ctx context.Context, settings Settings, query Query) (found []Summary, more bool, err error) {
	limit := min(max(query.Limit, 1), MostResults)
	talk, end, err := i.signedIn(ctx, settings)
	if err != nil {
		return nil, false, err
	}
	defer end()
	if err := examine(talk, query.Folder); err != nil {
		return nil, false, err
	}
	var uids []uint32
	ok, reason := talk.command(0, func(response reply) {
		listed, isSearch := strings.CutPrefix(response.text, "* SEARCH")
		if !isSearch {
			return
		}
		for _, field := range strings.Fields(listed) {
			if uid, err := strconv.ParseUint(field, 10, 32); err == nil {
				uids = append(uids, uint32(uid))
			}
		}
	}, criteria(query)...)
	if !ok {
		if reason == "" {
			return nil, false, errBroken
		}
		return nil, false, &ConnectionError{Message: "The mail server refused the search: " + reason}
	}
	// A folder's UIDs rise as messages arrive, so the highest are the
	// newest whatever the Date header of a message claims.
	slices.Sort(uids)
	slices.Reverse(uids)
	uids = slices.Compact(uids)
	if more = len(uids) > limit; more {
		uids = uids[:limit]
	}
	if len(uids) == 0 {
		return []Summary{}, false, nil
	}
	set := make([]string, len(uids))
	for index, uid := range uids {
		set[index] = strconv.FormatUint(uint64(uid), 10)
	}
	byUID := map[uint32]Summary{}
	ok, _ = talk.command(headerBudget, func(response reply) {
		if !strings.Contains(response.text, " FETCH (") || len(response.literals) == 0 {
			return
		}
		matched := fetchUID.FindStringSubmatch(response.text)
		if matched == nil {
			return
		}
		uid, _ := strconv.ParseUint(matched[1], 10, 32)
		summary := Summary{UID: uint32(uid), Unread: true}
		if flags := fetchFlags.FindStringSubmatch(response.text); flags != nil {
			summary.Unread = !slices.Contains(strings.Fields(flags[1]), `\Seen`)
		}
		if received := fetchReceived.FindStringSubmatch(response.text); received != nil {
			// "17-Jul-1996 02:44:25 -0700", the day padded with a space.
			summary.Date, _ = time.Parse("_2-Jan-2006 15:04:05 -0700", received[1])
		}
		headers := readHeaders(response.literals[0])
		summary.From, summary.To, summary.Subject = headers.Get("From"), headers.Get("To"), headers.Get("Subject")
		byUID[summary.UID] = summary
	}, "UID FETCH "+strings.Join(set, ",")+" (UID FLAGS INTERNALDATE BODY.PEEK[HEADER.FIELDS (FROM TO SUBJECT)])")
	if !ok {
		return nil, false, errBroken
	}
	found = make([]Summary, 0, len(uids))
	for _, uid := range uids {
		// A message removed between the search and the fetch is simply
		// not listed.
		if summary, fetched := byUID[uid]; fetched {
			found = append(found, summary)
		}
	}
	return found, more, nil
}

// decodedHeader is a header block whose values are read decoded.
type decodedHeader struct{ header mail.Header }

// words decodes RFC 2047 encoded words in any charset the web knows.
var words = mime.WordDecoder{CharsetReader: charset.NewReaderLabel}

// Get is a header's value as a person reads it: encoded words decoded,
// folding undone, and "" for a header that is absent.
func (h decodedHeader) Get(name string) string {
	raw := h.header.Get(name)
	decoded, err := words.DecodeHeader(raw)
	if err != nil {
		decoded = raw
	}
	return strings.Join(strings.Fields(strings.ToValidUTF8(decoded, "�")), " ")
}

// readHeaders reads a block of headers. One that cannot be parsed reads as
// no headers: the message is still listed, with nothing known about it.
func readHeaders(block []byte) decodedHeader {
	// A header block ends with an empty line; one cut short by a partial
	// fetch may not.
	message, err := mail.ReadMessage(bytes.NewReader(append(bytes.TrimRight(block, "\r\n"), "\r\n\r\n"...)))
	if err != nil {
		return decodedHeader{header: mail.Header{}}
	}
	return decodedHeader{header: message.Header}
}
