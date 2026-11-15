package notebookresearch

import (
	"context"
	"net/url"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/addsources"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/sourceidentity"
)

// What the chat's add_sources may read of a web page. A paper's identity
// goes to a fixed index in a fixed shape; a page's address is a request
// from this server to wherever the model chose, and the model may have been
// told what to choose by text it read. Two rules keep that from becoming a
// way to send what the conversation holds to someone else:
//
//   - A page is read only when the person wrote its address in this
//     conversation or approved the call (addsources.Request.PagesApproved).
//     The tool asks for every call naming a page the person did not write;
//     this holds the import to the same rule, so a call that was not asked
//     about reads no page the person did not give, even if the two ever
//     disagreed about which identity is a page.
//   - An address loses its query string and fragment unless the person
//     wrote that exact address in this conversation. Those are where data
//     is put in an address; a page the person pasted keeps them, because
//     then they are the person's, and many pages need their query.
//
// The path of an approved address is kept: it is what names the page, and
// the person saw it on the approval card.

// pageNeedsApproval is the reason a page in an unapproved call is not read.
const pageNeedsApproval = "Reading a web page needs the user's approval, and this call was not approved. Ask the user to approve adding this page."

// pageAddresses decides, for each page among the identities, whether it is
// read and at which address: as written when the person wrote it in this
// conversation, without its query and fragment when they only approved it.
// It answers the problem per identity for pages that may not be read at
// all. Papers are left alone.
func (c ChatSources) pageAddresses(ctx context.Context, request addsources.Request, identities []sourceidentity.Identity, problems []string) error {
	var pages []string
	for i, identity := range identities {
		if identity.Kind == sourceidentity.Page && problems[i] == "" {
			pages = append(pages, identity.Text)
		}
	}
	if len(pages) == 0 {
		return nil
	}
	theirs, err := c.UserWrote(ctx, request.UserID, request.ThreadID, pages)
	if err != nil {
		return err
	}
	for i := range identities {
		identity := &identities[i]
		if identity.Kind != sourceidentity.Page || problems[i] != "" || theirs[identity.Text] {
			continue
		}
		if !request.PagesApproved {
			problems[i] = pageNeedsApproval
			continue
		}
		identity.Address = withoutQuery(identity.Address)
	}
	return nil
}

// UserWrote is addsources.Adder's: of the addresses, those the account's
// user wrote in a text part of a message of this conversation, read under
// the thread's owner, so another account's thread answers nothing.
func (c ChatSources) UserWrote(ctx context.Context, userID string, threadID int64, addresses []string) (map[string]bool, error) {
	written, err := New(c.Imports.Pool).AddressesTheUserWrote(ctx, AddressesTheUserWroteParams{
		Addresses: addresses, UserID: userID, ThreadID: threadID,
	})
	if err != nil {
		return nil, err
	}
	theirs := make(map[string]bool, len(written))
	for _, address := range written {
		theirs[address] = true
	}
	return theirs, nil
}

// withoutQuery is the address with no query string and no fragment.
func withoutQuery(address *url.URL) *url.URL {
	bare := *address
	bare.RawQuery, bare.ForceQuery, bare.Fragment, bare.RawFragment = "", false, "", ""
	return &bare
}
