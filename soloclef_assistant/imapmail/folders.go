package imapmail

import (
	"context"
	"encoding/base64"
	"strings"
	"unicode/utf16"
)

// mostFolders is where listing a mailbox's folders stops.
const mostFolders = 200

// Folders signs in and counts the mailbox's folders: the test a person
// runs on a mailbox they are adding.
func (i IMAP) Folders(ctx context.Context, settings Settings) (int, error) {
	names, err := i.FolderNames(ctx, settings)
	return len(names), err
}

// FolderNames is the mailbox's folders by name, as a person reads them,
// in the server's order.
func (i IMAP) FolderNames(ctx context.Context, settings Settings) ([]string, error) {
	talk, end, err := i.signedIn(ctx, settings)
	if err != nil {
		return nil, err
	}
	defer end()
	names := []string{}
	// A folder's name may arrive as a literal; a name is short.
	ok, _ := talk.command(64<<10, func(response reply) {
		if !strings.HasPrefix(response.text, "* LIST ") || len(names) >= mostFolders {
			return
		}
		names = append(names, folderName(response))
	}, `LIST "" "*"`)
	if !ok {
		return nil, &ConnectionError{Message: "Could not list folders"}
	}
	return names, nil
}

// folderName is the name in one LIST response: the last item of
// `* LIST (flags) "delimiter" name`, where name is a quoted string, a bare
// word or a literal.
func folderName(response reply) string {
	if len(response.literals) > 0 {
		return decodeFolder(string(response.literals[len(response.literals)-1]))
	}
	text := response.text
	if strings.HasSuffix(text, `"`) {
		// Walk back to the quote that opens the name, past escaped ones.
		for open := len(text) - 2; open >= 0; open-- {
			if text[open] == '"' && (open == 0 || text[open-1] != '\\') {
				return decodeFolder(strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(text[open+1 : len(text)-1]))
			}
		}
	}
	return decodeFolder(text[strings.LastIndexByte(text, ' ')+1:])
}

// A folder's name crosses the wire in IMAP's modified UTF-7 (RFC 3501,
// 5.1.3): ASCII as itself, "&" as "&-", and everything else as UTF-16 in a
// base64 that uses "," for "/", between "&" and "-". A person reads and
// types the decoded name ("Entwürfe"), the server is sent the encoded one.

var folderBase64 = base64.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+,").WithPadding(base64.NoPadding)

// decodeFolder is a wire name as a person reads it. A name that is not
// valid modified UTF-7 is returned as it came: it is still the folder's
// name, and encodeFolder gives the same bytes back for it only if it holds
// no "&", so such a folder may not be selectable, which is said when it
// is tried.
func decodeFolder(wire string) string {
	if !strings.Contains(wire, "&") {
		return wire
	}
	var out strings.Builder
	for index := 0; index < len(wire); index++ {
		if wire[index] != '&' {
			out.WriteByte(wire[index])
			continue
		}
		end := strings.IndexByte(wire[index:], '-')
		if end < 0 {
			return wire
		}
		shifted := wire[index+1 : index+end]
		index += end
		if shifted == "" {
			out.WriteByte('&')
			continue
		}
		raw, err := folderBase64.DecodeString(shifted)
		if err != nil || len(raw)%2 != 0 {
			return wire
		}
		units := make([]uint16, len(raw)/2)
		for unit := range units {
			units[unit] = uint16(raw[2*unit])<<8 | uint16(raw[2*unit+1])
		}
		out.WriteString(string(utf16.Decode(units)))
	}
	return out.String()
}

// encodeFolder is a folder's name as the server is sent it.
func encodeFolder(name string) string {
	var out strings.Builder
	var shifted []uint16
	flush := func() {
		if len(shifted) == 0 {
			return
		}
		raw := make([]byte, 0, 2*len(shifted))
		for _, unit := range shifted {
			raw = append(raw, byte(unit>>8), byte(unit))
		}
		out.WriteString("&" + folderBase64.EncodeToString(raw) + "-")
		shifted = shifted[:0]
	}
	for _, character := range name {
		switch {
		case character == '&':
			flush()
			out.WriteString("&-")
		case character >= ' ' && character <= '~':
			flush()
			out.WriteRune(character)
		default:
			shifted = append(shifted, utf16.Encode([]rune{character})...)
		}
	}
	flush()
	return out.String()
}
