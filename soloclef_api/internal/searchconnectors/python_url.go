package searchconnectors

import (
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// This file is the part of Python's urllib.parse that
// app.mcp_security.secrets._mask_url runs a stored server address through:
// urlsplit, parse_qsl, urlencode and urlunsplit, as CPython 3.12 has them.
//
// It is a mirror of a library, which this tier does reluctantly
// (soloclef_api/AGENTS.md), and it is here because there is no way round it:
// every read of an MCP server masks its address, the mask is defined as what
// those four functions do to a string, and the string is whatever the column
// holds. net/url is not a substitute. It refuses addresses urlsplit takes
// (`https://h/?a=%zz`), takes one urlsplit refuses (`https://[1.2.3.4]/`),
// escapes differently, and keeps a query's spelling where urlencode rewrites
// it -- and each of those is either a different byte in a 200 or a 500 on one
// tier only.
//
// What keeps it honest is testdata/python_values.json: 4,006 addresses, most
// of them generated, each with what Python's own _mask_url answered or the
// fact that it raised. TestAddressesAreMaskedAsPythonMasksThem replays all of
// them.

// errAddressRefused is the ValueError urlsplit raises. Nothing in
// mask_server_config catches it, so it reaches the route's own
// `except Exception` and is that route's 500.
var errAddressRefused = errors.New("searchconnectors: urlsplit refuses this address")

// errUnencodable is the UnicodeEncodeError urllib's quote raises for a lone
// surrogate. It is a ValueError too, and ends the same way.
var errUnencodable = errors.New("searchconnectors: a lone surrogate cannot be percent-encoded")

// splitAddress is the five parts urlsplit names.
type splitAddress struct {
	scheme, netloc, path, query, fragment string
}

// The characters urlsplit strips from the front of an address: every C0
// control and the space (_WHATWG_C0_CONTROL_OR_SPACE). Only the front; a
// trailing space stays, and ends up percent-encoded as "+" in a query.
const leadingJunk = "\x00\x01\x02\x03\x04\x05\x06\x07\x08\t\n\x0b\x0c\r\x0e\x0f" +
	"\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f "

// urlSplit is urllib.parse.urlsplit(url).
func urlSplit(address string) (splitAddress, error) {
	address = strings.TrimLeft(address, leadingJunk)
	// _UNSAFE_URL_BYTES_TO_REMOVE: a tab, a return or a newline anywhere.
	address = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(address)

	var parts splitAddress
	// A scheme is a run of scheme characters before the first colon that
	// starts with an ASCII letter. `1http:` and `é:` are not schemes, and the
	// colon then stays in the path.
	if colon := strings.IndexByte(address, ':'); colon > 0 && isASCIILetter(address[0]) {
		if isSchemeText(address[:colon]) {
			parts.scheme = strings.ToLower(address[:colon]) // ASCII by construction
			address = address[colon+1:]
		}
	}
	if strings.HasPrefix(address, "//") {
		// _splitnetloc: the host part runs to the first of "/", "?" or "#".
		end := len(address)
		if cut := strings.IndexAny(address[2:], "/?#"); cut >= 0 {
			end = cut + 2
		}
		parts.netloc, address = address[2:end], address[end:]
		opens := strings.Contains(parts.netloc, "[")
		closes := strings.Contains(parts.netloc, "]")
		if opens != closes {
			return splitAddress{}, errAddressRefused // "Invalid IPv6 URL"
		}
		if opens && closes {
			if err := checkBracketedNetloc(parts.netloc); err != nil {
				return splitAddress{}, err
			}
		}
	}
	if hash := strings.IndexByte(address, '#'); hash >= 0 {
		address, parts.fragment = address[:hash], address[hash+1:]
	}
	if question := strings.IndexByte(address, '?'); question >= 0 {
		address, parts.query = address[:question], address[question+1:]
	}
	if err := checkNetloc(parts.netloc); err != nil {
		return splitAddress{}, err
	}
	parts.path = address
	return parts, nil
}

func isASCIILetter(character byte) bool {
	return (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
}

// isSchemeText is "every character is in scheme_chars": ASCII letters, digits
// and "+", "-", ".".
func isSchemeText(text string) bool {
	for index := 0; index < len(text); index++ {
		character := text[index]
		if !isASCIILetter(character) && (character < '0' || character > '9') &&
			character != '+' && character != '-' && character != '.' {
			return false
		}
	}
	return true
}

// checkBracketedNetloc is _check_bracketed_netloc: nothing may stand before
// the bracket, nothing but a port after it, and what is inside must be an
// IPv6 address or an "IPvFuture" literal.
func checkBracketedNetloc(netloc string) error {
	hostAndPort := netloc
	if at := strings.LastIndexByte(netloc, '@'); at >= 0 {
		hostAndPort = netloc[at+1:]
	}
	var host string
	if before, bracketed, found := strings.Cut(hostAndPort, "["); found {
		if before != "" {
			return errAddressRefused
		}
		inside, port, _ := strings.Cut(bracketed, "]")
		if port != "" && !strings.HasPrefix(port, ":") {
			return errAddressRefused
		}
		host = inside
	} else {
		// The brackets are in the login part (`[x]@host`), so what Python
		// checks is the plain host -- which is then never an IPv6 address.
		host, _, _ = strings.Cut(hostAndPort, ":")
	}
	return checkBracketedHost(host)
}

// checkBracketedHost is _check_bracketed_host.
func checkBracketedHost(host string) error {
	if strings.HasPrefix(host, "v") {
		// re.match(r"\Av[a-fA-F0-9]+\..+\Z"): hex digits, a dot, and at least
		// one more character. (`.` would refuse a newline, and urlsplit has
		// already removed every newline.)
		digits := 0
		rest := host[1:]
		for digits < len(rest) && isHexDigit(rest[digits]) {
			digits++
		}
		if digits == 0 || len(rest) < digits+2 || rest[digits] != '.' {
			return errAddressRefused
		}
		return nil
	}
	// ipaddress.ip_address(host), then "An IPv4 address cannot be in
	// brackets": only a valid IPv6 address passes. A string is never both.
	if !isIPv6Address(host) {
		return errAddressRefused
	}
	return nil
}

func isHexDigit(character byte) bool {
	return (character >= '0' && character <= '9') ||
		(character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')
}

// isIPv6Address is "ipaddress.IPv6Address(text) does not raise", rule for
// rule. net/netip is close and not the same -- it has no 45-character limit
// and reads a zone its own way -- and the difference here would be a 500 on
// one tier.
func isIPv6Address(text string) bool {
	// _split_scope_id: at most one "%", with something after it.
	address, scope, scoped := strings.Cut(text, "%")
	if scoped && (scope == "" || strings.Contains(scope, "%")) {
		return false
	}
	if address == "" || utf8.RuneCountInString(address) > 45 {
		return false
	}
	// split(':', maxsplit=9): at most ten parts, the last keeping its colons.
	parts := strings.SplitN(address, ":", 10)
	if len(parts) < 3 {
		return false
	}
	if last := parts[len(parts)-1]; strings.Contains(last, ".") {
		// An IPv4-style tail becomes two hextets. Their values do not matter
		// here, only that they are valid ones.
		if !isIPv4Address(last) {
			return false
		}
		parts = append(parts[:len(parts)-1], "0", "0")
	}
	if len(parts) > 9 {
		return false
	}
	// A "::" is an empty part that is not at either end; there may be one.
	skipIndex := -1
	for index := 1; index < len(parts)-1; index++ {
		if parts[index] == "" {
			if skipIndex >= 0 {
				return false
			}
			skipIndex = index
		}
	}
	var high, low int
	if skipIndex >= 0 {
		high = skipIndex
		low = len(parts) - skipIndex - 1
		if parts[0] == "" {
			high--
			if high != 0 {
				return false // a leading ":" is only allowed as part of "::"
			}
		}
		if parts[len(parts)-1] == "" {
			low--
			if low != 0 {
				return false
			}
		}
		if 8-(high+low) < 1 {
			return false
		}
	} else {
		if len(parts) != 8 || parts[0] == "" || parts[len(parts)-1] == "" {
			return false
		}
		high, low = len(parts), 0
	}
	for index := 0; index < high; index++ {
		if !isHextet(parts[index]) {
			return false
		}
	}
	for index := len(parts) - low; index < len(parts); index++ {
		if !isHextet(parts[index]) {
			return false
		}
	}
	return true
}

// isHextet is _parse_hextet: one to four ASCII hex digits. An empty part
// passes Python's character test and then fails the conversion of an empty
// string to a number.
func isHextet(text string) bool {
	if text == "" || len(text) > 4 {
		return false
	}
	for index := 0; index < len(text); index++ {
		if !isHexDigit(text[index]) {
			return false
		}
	}
	return true
}

// isIPv4Address is "ipaddress.IPv4Address(text) does not raise": four
// decimal octets, ASCII digits only, at most three of them, none with a
// leading zero, none above 255.
func isIPv4Address(text string) bool {
	octets := strings.Split(text, ".")
	if len(octets) != 4 {
		return false
	}
	for _, octet := range octets {
		if octet == "" || len(octet) > 3 {
			return false
		}
		value := 0
		for index := 0; index < len(octet); index++ {
			if octet[index] < '0' || octet[index] > '9' {
				return false
			}
			value = value*10 + int(octet[index]-'0')
		}
		if (octet != "0" && octet[0] == '0') || value > 255 {
			return false
		}
	}
	return true
}

// checkNetloc is _checknetloc: a host part that is not ASCII is refused when
// NFKC normalisation turns one of its characters into a delimiter -- U+2100
// "℀" becomes "a/c", which would be a path the split never saw.
func checkNetloc(netloc string) error {
	if netloc == "" || isASCII(netloc) {
		return nil
	}
	stripped := strings.NewReplacer("@", "", ":", "", "#", "", "?", "").Replace(netloc)
	normalised := norm.NFKC.String(stripped)
	if normalised == stripped {
		return nil
	}
	if strings.ContainsAny(normalised, "/?#@:") {
		return errAddressRefused
	}
	return nil
}

func isASCII(text string) bool {
	for index := 0; index < len(text); index++ {
		if text[index] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// urlUnsplit is urllib.parse.urlunsplit. It is not the inverse of the split:
// `https:/mcp` comes back as `https:///mcp`, because a scheme that "uses a
// netloc" gets its two slashes whether or not there was a host.
func urlUnsplit(parts splitAddress) string {
	address := parts.path
	switch {
	case parts.netloc != "":
		if address != "" && !strings.HasPrefix(address, "/") {
			address = "/" + address
		}
		address = "//" + parts.netloc + address
	case strings.HasPrefix(address, "//"):
		address = "//" + address
	case parts.scheme != "" && usesNetloc[parts.scheme] &&
		(address == "" || strings.HasPrefix(address, "/")):
		address = "//" + address
	}
	if parts.scheme != "" {
		address = parts.scheme + ":" + address
	}
	if parts.query != "" {
		address += "?" + parts.query
	}
	if parts.fragment != "" {
		address += "#" + parts.fragment
	}
	return address
}

// usesNetloc is urllib.parse.uses_netloc, less its empty entry: a scheme is
// only looked up when there is one.
var usesNetloc = map[string]bool{
	"ftp": true, "http": true, "gopher": true, "nntp": true, "telnet": true,
	"imap": true, "wais": true, "file": true, "mms": true, "https": true,
	"shttp": true, "snews": true, "prospero": true, "rtsp": true, "rtsps": true,
	"rtspu": true, "rsync": true, "svn": true, "svn+ssh": true, "sftp": true,
	"nfs": true, "git": true, "git+ssh": true, "ws": true, "wss": true,
	"itms-services": true, "ssh": true, "hg": true, "bzr": true,
}

// queryPair is one (name, value) of parse_qsl's answer.
type queryPair struct {
	name, value string
}

// parseQuery is parse_qsl(query, keep_blank_values=True): split on "&", drop
// the empty pieces, cut each at its first "=", and unquote both halves. A
// piece with no "=" is a name with an empty value.
func parseQuery(query string) []queryPair {
	var pairs []queryPair
	for _, piece := range strings.Split(query, "&") {
		if piece == "" {
			continue
		}
		name, value, _ := strings.Cut(piece, "=")
		pairs = append(pairs, queryPair{name: unquotePlus(name), value: unquotePlus(value)})
	}
	return pairs
}

// unquotePlus is urllib.parse.unquote_plus with its defaults.
//
// A "+" is a space. Percent escapes are then decoded *within each run of
// ASCII characters*, and the bytes they give are read as UTF-8 with
// errors="replace" -- so `%C3` followed by a literal "é" is a broken sequence
// and a replacement character, not the two bytes joined. A "%" that is not
// followed by two hex digits stays as it is.
func unquotePlus(text string) string {
	text = strings.ReplaceAll(text, "+", " ")
	if !strings.Contains(text, "%") {
		return text
	}
	var out strings.Builder
	for len(text) > 0 {
		run := 0
		for run < len(text) && text[run] < utf8.RuneSelf {
			run++
		}
		if run > 0 {
			out.WriteString(decodeReplacing(unquoteBytes(text[:run])))
			text = text[run:]
			continue
		}
		for run < len(text) && text[run] >= utf8.RuneSelf {
			run++
		}
		out.WriteString(text[:run])
		text = text[run:]
	}
	return out.String()
}

// unquoteBytes is _unquote_impl: every "%XX" becomes its byte.
func unquoteBytes(text string) []byte {
	pieces := strings.Split(text, "%")
	out := []byte(pieces[0])
	for _, piece := range pieces[1:] {
		if len(piece) >= 2 && isHexDigit(piece[0]) && isHexDigit(piece[1]) {
			out = append(out, hexValue(piece[0])<<4|hexValue(piece[1]))
			out = append(out, piece[2:]...)
			continue
		}
		out = append(out, '%')
		out = append(out, piece...)
	}
	return out
}

func hexValue(character byte) byte {
	switch {
	case character >= 'a':
		return character - 'a' + 10
	case character >= 'A':
		return character - 'A' + 10
	}
	return character - '0'
}

// decodeReplacing is bytes.decode("utf-8", "replace").
//
// Python replaces each *maximal subpart* of an ill-formed sequence with one
// U+FFFD: the two bytes `E2 80` with nothing after them are one replacement
// character, where Go's own decoding gives one per byte. Which bytes may
// continue a sequence is the table in the Unicode standard (3.9, D92), so an
// encoded surrogate (`ED A0 80`) breaks after its first byte.
func decodeReplacing(data []byte) string {
	var out strings.Builder
	for index := 0; index < len(data); {
		lead := data[index]
		if lead < 0x80 {
			out.WriteByte(lead)
			index++
			continue
		}
		var need int
		low, high := byte(0x80), byte(0xbf)
		switch {
		case lead >= 0xc2 && lead <= 0xdf:
			need = 1
		case lead == 0xe0:
			need, low = 2, 0xa0
		case lead >= 0xe1 && lead <= 0xec, lead == 0xee, lead == 0xef:
			need = 2
		case lead == 0xed:
			need, high = 2, 0x9f
		case lead == 0xf0:
			need, low = 3, 0x90
		case lead >= 0xf1 && lead <= 0xf3:
			need = 3
		case lead == 0xf4:
			need, high = 3, 0x8f
		default:
			// A continuation byte with nothing to continue, or a byte no
			// sequence starts with.
			out.WriteRune(utf8.RuneError)
			index++
			continue
		}
		taken := 1
		for taken <= need && index+taken < len(data) {
			next := data[index+taken]
			if next < low || next > high {
				break
			}
			// Only the second byte has a narrowed range.
			low, high = 0x80, 0xbf
			taken++
		}
		if taken == need+1 {
			out.Write(data[index : index+taken])
		} else {
			out.WriteRune(utf8.RuneError)
		}
		index += taken
	}
	return out.String()
}

// encodeQuery is urlencode(pairs, safe=MASK).
//
// The `safe` argument does nothing: urllib drops every non-ASCII character
// from it before use, and the mask is four of them. So the mask is
// percent-encoded like any other text, which is why a masked address reads
// `api_key=%E2%80%A2%E2%80%A2%E2%80%A2%E2%80%A26789`.
//
// A lone surrogate in a name or in a value that is still there after masking
// cannot be encoded: quote() encodes strictly and raises.
func encodeQuery(pairs []queryPair) (string, error) {
	encoded := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		if hasLoneSurrogate(pair.name) || hasLoneSurrogate(pair.value) {
			return "", errUnencodable
		}
		encoded = append(encoded, quotePlus(pair.name)+"="+quotePlus(pair.value))
	}
	return strings.Join(encoded, "&"), nil
}

// quotePlus is urllib.parse.quote_plus(text, safe=""): the unreserved
// characters stay, a space is "+", and every other byte of the UTF-8 is "%XX"
// in capitals.
func quotePlus(text string) string {
	const hexDigits = "0123456789ABCDEF"
	var out strings.Builder
	for index := 0; index < len(text); index++ {
		character := text[index]
		switch {
		case isASCIILetter(character), character >= '0' && character <= '9',
			character == '_', character == '.', character == '-', character == '~':
			out.WriteByte(character)
		case character == ' ':
			out.WriteByte('+')
		default:
			out.WriteByte('%')
			out.WriteByte(hexDigits[character>>4])
			out.WriteByte(hexDigits[character&0x0f])
		}
	}
	return out.String()
}
