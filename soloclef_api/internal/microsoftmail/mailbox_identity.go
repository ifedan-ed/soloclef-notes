package microsoftmail

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
)

// mailbox is which mailbox a grant is for.
type mailbox struct {
	// Address is the IMAP user name: the address XOAUTH2 signs in as.
	Address string
	// Subject is the stable id of the Microsoft account (tenant and
	// object): a reconnect updates the same mailbox even if its address
	// changed.
	Subject string
}

// errUnknownMailbox: the id_token names no usable mailbox.
var errUnknownMailbox = errors.New("microsoftmail: the id_token names no mailbox this app can use")

// address is a claim that is one email address, or "".
func address(claim any) string {
	text, isText := claim.(string)
	if !isText || !strings.Contains(text, "@") || strings.IndexFunc(text, unicode.IsSpace) >= 0 {
		return ""
	}
	return text
}

// mailboxFromIDToken reads which mailbox a fresh grant is for.
//
// The id_token came straight from Microsoft's token endpoint, over TLS, in
// exchange for this deployment's client secret, so its signature is not
// checked again (OpenID Connect Core 3.1.3.7 allows that for a token
// received this way); its audience must still be this app. It is read once
// at connect time and never stored.
func mailboxFromIDToken(idToken, clientID string) (mailbox, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return mailbox{}, errUnknownMailbox
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return mailbox{}, errUnknownMailbox
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return mailbox{}, errUnknownMailbox
	}
	// `aud` is one name or a list of them.
	audiences, isList := claims["aud"].([]any)
	if !isList {
		audiences = []any{claims["aud"]}
	}
	ours := false
	for _, audience := range audiences {
		ours = ours || audience == clientID
	}
	if !ours {
		return mailbox{}, errUnknownMailbox
	}
	// `email` is the mailbox's address where Microsoft has one; for work
	// accounts `preferred_username` is the sign-in name, which Exchange
	// also accepts.
	found := address(claims["email"])
	if found == "" {
		found = address(claims["preferred_username"])
	}
	if found == "" {
		return mailbox{}, errUnknownMailbox
	}
	text := func(key string) string {
		value, _ := claims[key].(string)
		return value
	}
	// `sub` (per app, also stable) stands in if `oid` is ever missing.
	object := text("oid")
	if object == "" {
		object = text("sub")
	}
	return mailbox{Address: found, Subject: text("tid") + ":" + object}, nil
}
