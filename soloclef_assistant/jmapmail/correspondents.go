package jmapmail

import (
	"context"
	"strconv"
	"strings"
)

// candidatesPerAddress is how many sent messages are looked at for each
// address. One that really went to the address is enough to know it; a
// few are fetched because the server's header filter is a text match
// (RFC 8621 4.4.1: "looks for the text"), so "ada@example.org" also finds
// mail to "nada@example.org", and the hits are checked exactly below.
const candidatesPerAddress = 5

// NeverMailed is the addresses, of those given, that no message in the
// mailbox's Sent folder went to (in To, Cc or Bcc). It is how the send
// guard (soloclef_assistant/maildrafts) knows a recipient is new to this
// mailbox, which is one of the things it asks the owner to confirm twice.
//
// It costs a few requests, not one per message: the Sent folder's id, then
// one query per address, as many to a request as the server allows, then
// the hits' recipient headers in one /get, compared exactly and without
// case. The answer keeps the spelling the addresses were given in.
func (c Client) NeverMailed(ctx context.Context, session Session, credential Credential, addresses []string) ([]string, error) {
	if len(addresses) == 0 {
		return []string{}, nil
	}
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	folders, err := c.mailboxes(ctx, session, credential)
	if err != nil {
		return nil, err
	}
	sent, found := ByRole(folders, "sent")
	if !found {
		return nil, &Failure{Message: "The mailbox has no Sent folder to check its recipients against"}
	}
	// One query per address, in requests of as many calls as allowed.
	var ids []string
	seen := map[string]bool{}
	for start := 0; start < len(addresses); start += session.MaxCallsInRequest {
		batch := addresses[start:min(start+session.MaxCallsInRequest, len(addresses))]
		calls := make([]invocation, 0, len(batch))
		for index, address := range batch {
			calls = append(calls, invocation{"Email/query", map[string]any{
				"accountId": session.AccountID,
				"filter": map[string]any{"operator": "AND", "conditions": []any{
					map[string]any{"inMailbox": sent.ID},
					map[string]any{"operator": "OR", "conditions": []any{
						map[string]any{"to": address}, map[string]any{"cc": address}, map[string]any{"bcc": address},
					}},
				}},
				"limit": candidatesPerAddress,
			}, "q" + strconv.Itoa(index)})
		}
		got, err := c.call(ctx, session, credential, mailUsing, calls...)
		if err != nil {
			return nil, err
		}
		for index := range batch {
			var queried struct {
				IDs []string `json:"ids"`
			}
			if err := got.decode("q"+strconv.Itoa(index), "Email/query", &queried); err != nil {
				return nil, err
			}
			for _, id := range queried.IDs {
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
	}
	// The hits' recipients, compared exactly.
	mailed := map[string]bool{}
	for start := 0; start < len(ids); start += session.MaxObjectsInGet {
		chunk := ids[start:min(start+session.MaxObjectsInGet, len(ids))]
		got, err := c.call(ctx, session, credential, mailUsing, invocation{"Email/get", map[string]any{
			"accountId": session.AccountID, "ids": chunk, "properties": []string{"id", "to", "cc", "bcc"},
		}, "g"})
		if err != nil {
			return nil, err
		}
		var fetched struct {
			List []struct {
				To, Cc, Bcc []Address
			} `json:"list"`
		}
		if err := got.decode("g", "Email/get", &fetched); err != nil {
			return nil, err
		}
		for _, email := range fetched.List {
			for _, list := range [][]Address{email.To, email.Cc, email.Bcc} {
				for _, address := range list {
					mailed[strings.ToLower(strings.TrimSpace(address.Email))] = true
				}
			}
		}
	}
	out := []string{}
	for _, address := range addresses {
		if !mailed[strings.ToLower(strings.TrimSpace(address))] {
			out = append(out, address)
		}
	}
	return out, nil
}
