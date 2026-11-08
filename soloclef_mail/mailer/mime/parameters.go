package mime

import (
	"strings"
)

// foldContentType writes a Content-Type with its boundary, folding the
// parameter onto its own line when the whole header will not fit.
//
// This is _fold_mime_parameters at the one size that occurs: either the header
// fits in 78 characters or the parameter goes on a continuation line indented
// one space. multipart/mixed fits and multipart/alternative does not, which is
// why the two look different in the same message.
func foldContentType(subtype, boundary string) string {
	const name = "Content-Type: "
	parameter := "boundary=\"" + boundary + "\""
	if len(name)+len(subtype)+len("; ")+len(parameter) <= maxLineLength {
		return name + subtype + "; " + parameter + "\r\n"
	}
	return name + subtype + ";\r\n " + parameter + "\r\n"
}

// foldContentDisposition names the attached file: quoted when the name is
// plain, and RFC 2231 when it is not.
//
// email.utils.encode_rfc2231 is percent-encoding with nothing in the safe set,
// over urllib's always-safe letters, digits and "_.-~". A client that cannot
// read RFC 2231 shows the encoded text as the filename, which is the price
// Python pays here too -- and matching it matters more than improving it,
// because a licence file saved under a different name is a support ticket.
func foldContentDisposition(filename string) string {
	const name = "Content-Disposition: attachment; "
	if isASCII([]byte(filename)) && !strings.ContainsAny(filename, "\"\\\r\n") {
		return name + "filename=\"" + filename + "\"\r\n"
	}
	return name + "filename*=utf-8''" + percentEncode(filename) + "\r\n"
}

// alwaysSafe is urllib.parse.quote's own always-safe set, with safe=”.
const alwaysSafe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.-~"

func percentEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(alwaysSafe, s[i]) >= 0 {
			b.WriteByte(s[i])
			continue
		}
		b.WriteString("%" + upperHex(s[i]))
	}
	return b.String()
}
