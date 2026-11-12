package imapmail

import (
	"bufio"
	"net"
	"strconv"
	"strings"
)

// lineLimit bounds one line of a server's reply held in memory. A search
// of a very large folder answers every match on one line, which is why it
// is megabytes and not the few kilobytes any other line needs.
const lineLimit = 8 << 20

// mostLines bounds one command's reply.
const mostLines = 10_000

// session is one IMAP conversation: tagged commands out, replies back.
type session struct {
	conn   net.Conn
	reader *bufio.Reader
	next   int
}

func newSession(conn net.Conn) *session {
	return &session{conn: conn, reader: bufio.NewReaderSize(conn, 64<<10)}
}

// reply is one response of the server: its text, and the literals that
// were sent inside it, in order. A literal's bytes are never in the text
// (its "{n}" marker stays where it was), so nothing a message contains can
// be read as part of the server's own words.
type reply struct {
	text     string
	literals [][]byte
}

// line reads one line, without its line ending. A line longer than the
// limit ends the conversation.
func (s *session) line() (string, bool) {
	var whole []byte
	for {
		part, err := s.reader.ReadSlice('\n')
		whole = append(whole, part...)
		if err == nil {
			return strings.TrimRight(string(whole), "\r\n"), true
		}
		if err != bufio.ErrBufferFull || len(whole) > lineLimit {
			return "", false
		}
	}
}

// literalSize reads the "{n}" a line ends with when a literal follows it.
func literalSize(line string) (int, bool) {
	if !strings.HasSuffix(line, "}") {
		return 0, false
	}
	open := strings.LastIndexByte(line, '{')
	if open < 0 {
		return 0, false
	}
	size, err := strconv.Atoi(line[open+1 : len(line)-1])
	return size, err == nil && size >= 0
}

// read reads one whole response: a line, and for as long as the line ends
// by announcing a literal, the literal and the line after it. budget is
// how many literal bytes the caller will hold for this response; more
// ends the conversation, because the rest of it cannot be skipped
// without being read.
func (s *session) read(budget int) (reply, bool) {
	var out reply
	for {
		line, read := s.line()
		if !read {
			return reply{}, false
		}
		out.text += line
		size, follows := literalSize(line)
		if !follows {
			return out, true
		}
		if size > budget {
			return reply{}, false
		}
		budget -= size
		held := make([]byte, size)
		for filled := 0; filled < size; {
			count, err := s.reader.Read(held[filled:])
			if err != nil {
				return reply{}, false
			}
			filled += count
		}
		out.literals = append(out.literals, held)
	}
}

// greeted reads the server's first line, which an IMAP server begins
// "* OK" (or "* PREAUTH", which is not a server to type a password into).
func (s *session) greeted() bool {
	line, read := s.line()
	return read && strings.HasPrefix(line, "* OK")
}

// literal is a string sent to the server as a literal: its length first,
// then, once the server says to go on, its bytes. It is how a value that a
// quoted string cannot hold (a quote, a line break, a letter outside
// ASCII) is sent without being able to end the command it is in.
type literal string

// astring is text as a command's argument: quoted when a quoted string
// can hold it, a literal otherwise.
func astring(text string) any {
	for index := 0; index < len(text); index++ {
		if c := text[index]; c < ' ' || c > '~' {
			return literal(text)
		}
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(text) + `"`
}

// command sends one command and reads to its tagged reply. parts are the
// command's pieces in order: a string is sent as it is, a literal as one.
// ok is whether the server said OK; reason is its text when it said NO or
// BAD, made safe to show, and "" when the conversation broke. untagged is
// given each response before the tagged one; budget bounds the literals
// of each.
func (s *session) command(budget int, untagged func(reply), parts ...any) (ok bool, reason string) {
	s.next++
	tag := "a" + strconv.Itoa(s.next)
	// answer reads responses until the tagged one (done) or, when a
	// literal is waiting to be sent, the server's "+" (done false).
	answer := func(waiting bool) (done, ok bool, reason string) {
		for count := 0; count < mostLines; count++ {
			response, read := s.read(budget)
			if !read {
				return true, false, ""
			}
			if rest, tagged := strings.CutPrefix(response.text, tag+" "); tagged {
				if strings.HasPrefix(rest, "OK") {
					return true, true, ""
				}
				return true, false, shown(rest)
			}
			if waiting && strings.HasPrefix(response.text, "+") {
				return false, false, ""
			}
			if untagged != nil {
				untagged(response)
			}
		}
		return true, false, ""
	}
	pending := tag
	for _, part := range parts {
		switch piece := part.(type) {
		case string:
			pending += " " + piece
		case literal:
			pending += " {" + strconv.Itoa(len(piece)) + "}\r\n"
			if _, err := s.conn.Write([]byte(pending)); err != nil {
				return false, ""
			}
			if done, ok, reason := answer(true); done {
				// The server refused the command before taking the
				// literal; nothing more of it is sent.
				return ok, reason
			}
			pending = string(piece)
			if _, err := s.conn.Write([]byte(pending)); err != nil {
				return false, ""
			}
			pending = ""
		}
	}
	if _, err := s.conn.Write([]byte(pending + "\r\n")); err != nil {
		return false, ""
	}
	_, ok, reason = answer(false)
	return ok, reason
}

// shown is a server's reason as it may be repeated to a person: printable
// characters only, and not much of it.
func shown(reason string) string {
	var out strings.Builder
	for _, character := range reason {
		if out.Len() >= 200 {
			break
		}
		if character >= ' ' && character != 0x7f && character != 0xfffd {
			out.WriteRune(character)
		}
	}
	if out.Len() == 0 {
		return "no reason given"
	}
	return out.String()
}
