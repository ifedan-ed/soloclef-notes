package jmapmail

import "context"

// Identity is an address the account may send as (RFC 8621 6).
type Identity struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// submissionUsing is the capabilities a request that sends, or that reads
// the identities a send needs, declares.
var submissionUsing = []string{capabilityCore, capabilityMail, capabilitySubmission}

// Identities is every identity of the session's account.
func (c Client) Identities(ctx context.Context, session Session, credential Credential) ([]Identity, error) {
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	got, err := c.call(ctx, session, credential, submissionUsing,
		invocation{"Identity/get", map[string]any{"accountId": session.AccountID, "ids": nil}, "i"})
	if err != nil {
		return nil, err
	}
	var listed struct {
		List []Identity `json:"list"`
	}
	if err := got.decode("i", "Identity/get", &listed); err != nil {
		return nil, err
	}
	return listed.List, nil
}
