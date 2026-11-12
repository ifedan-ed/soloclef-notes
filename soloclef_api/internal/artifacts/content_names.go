package artifacts

import (
	"path"
	"strings"
	"unicode"
)

// This file is the names a browser is told: what a downloaded file is called,
// and whether a file may be shown in the page or must be saved.

// inlineTypes are the media types a browser may *display* from this origin.
//
// A stored file is something a user or the agent wrote, so rendering one in
// the page is running somebody else's content with this site's cookies. PDF
// and MP4 are the two whose built-in viewers execute nothing; every other
// type is sent as an attachment. The list is widened one named type at a
// time, with a viewer that needs it -- never by pattern: `image/*` would let
// in SVG, which is a script.
var inlineTypes = map[string]bool{
	"application/pdf": true,
	"video/mp4":       true,
}

// fallbackType is what a file is sent as when its row names no type: bytes,
// to be saved and not interpreted.
const fallbackType = "application/octet-stream"

// contentDisposition is the header that names a file: an ASCII name every
// client understands, and the real name in the RFC 5987 form every current
// browser prefers (`filename*`). The web app reads the second and falls back
// to the first (features/artifacts/api/artifact-download.ts).
func contentDisposition(filename string, inline bool) string {
	mode := "attachment"
	if inline {
		mode = "inline"
	}
	return mode + `; filename="` + asciiName(filename) + `"; filename*=UTF-8''` + encodeForHeader(filename)
}

// asciiName is a name with everything a quoted header value cannot safely
// hold taken out: bytes outside ASCII, control characters (a line break here
// would start a new header), and the two characters that mean something
// inside quotes.
//
// When nothing is left of the part before the extension -- a title written
// entirely in Arabic, say -- the name is "download" plus the extension. The
// Python helper kept only the extension, so such a file arrived named
// ".pdf", which most systems hide.
func asciiName(filename string) string {
	var kept strings.Builder
	for _, character := range filename {
		if character >= 0x20 && character < 0x7f && character != '"' && character != '\\' {
			kept.WriteRune(character)
		}
	}
	name := strings.TrimSpace(kept.String())
	extension := path.Ext(name)
	if strings.TrimSpace(strings.TrimSuffix(name, extension)) == "" {
		return "download" + extension
	}
	return name
}

// encodeForHeader percent-encodes a name for `filename*=UTF-8”...`. RFC 5987
// allows letters, digits and a short list of marks to stand as themselves;
// every other byte is escaped. `/` is escaped too: Python's `quote` leaves it
// alone by default, and a slash in a suggested filename is at best ignored.
func encodeForHeader(text string) string {
	const hex = "0123456789ABCDEF"
	var encoded strings.Builder
	for index := 0; index < len(text); index++ {
		character := text[index]
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			strings.IndexByte("!#$&+-.^_`|~", character) >= 0:
			encoded.WriteByte(character)
		default:
			encoded.WriteByte('%')
			encoded.WriteByte(hex[character>>4])
			encoded.WriteByte(hex[character&0x0f])
		}
	}
	return encoded.String()
}

// The download is named after the artifact's *title*, which is what the
// person sees in the Studio, with the extension of the stored file: a deck
// titled "Cell biology" downloads as "Cell biology.json" whatever the agent
// called the file it wrote.
const (
	// stemLimit keeps a name comfortably inside every filesystem's limit
	// with room for the extension. It counts characters, not bytes.
	stemLimit = 80
	// extensionLimit is the longest thing still believed to be an extension
	// ("." included); past it, a dot in a filename is just a dot.
	extensionLimit = 16
	nameless       = "artifact"
)

// downloadName is the title made safe as a filename, plus the stored file's
// extension.
func downloadName(title, storedFilename string) string {
	extension := strings.ToLower(path.Ext(storedFilename))
	// ".env" is a name, not a nameless file with the extension "env": an
	// extension is only one when something stands before its dot.
	if !plainExtension(extension) || len(extension) == len(path.Base(storedFilename)) {
		extension = ""
	}
	// A title that already ends in the extension ("Budget.xlsx") must not
	// come out as "Budget_xlsx.xlsx".
	if extension != "" && len(title) >= len(extension) &&
		strings.EqualFold(title[len(title)-len(extension):], extension) {
		title = title[:len(title)-len(extension)]
	}
	return safeStem(title) + extension
}

// markdownName is the name of the download for an artifact with no file:
// its document, as Markdown.
func markdownName(title string) string { return safeStem(title) + ".md" }

// plainExtension says the text is a dot followed by one to fifteen ASCII
// letters or digits.
func plainExtension(extension string) bool {
	if len(extension) < 2 || len(extension) > extensionLimit || extension[0] != '.' {
		return false
	}
	for _, character := range extension[1:] {
		isLetter := character >= 'a' && character <= 'z'
		isDigit := character >= '0' && character <= '9'
		if !isLetter && !isDigit {
			return false
		}
	}
	return true
}

// safeStem keeps letters and digits of any script, spaces, hyphens and
// underscores, and turns everything else into an underscore -- which is every
// character that is a path separator, a wildcard or reserved on some system.
func safeStem(title string) string {
	var stem strings.Builder
	kept := 0
	for _, character := range strings.TrimSpace(title) {
		if kept == stemLimit {
			break
		}
		if unicode.IsLetter(character) || unicode.IsNumber(character) ||
			character == ' ' || character == '-' || character == '_' {
			stem.WriteRune(character)
		} else {
			stem.WriteByte('_')
		}
		kept++
	}
	// Trimmed again: the cut may have landed just after a space.
	if name := strings.TrimSpace(stem.String()); name != "" {
		return name
	}
	return nameless
}
