package notebooklinks

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// ownerCache is every owner's answer: theirs alone, and stale the moment
// they press Share or Stop.
const ownerCache = "private, no-store"

// ownerLink is a link as its owner reads it. There is one owner, so there
// is no "made by" and no "may revoke": the owner made it and may.
type ownerLink struct {
	ID            int64      `json:"id"`
	NotebookID    int64      `json:"notebook_id"`
	NotebookTitle string     `json:"notebook_title"`
	NotebookIcon  *string    `json:"notebook_icon"`
	ShareToken    string     `json:"share_token"`
	PublicURL     string     `json:"public_url"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	IsExpired     bool       `json:"is_expired"`
}

func (r Routes) describe(row LinksOfOwnerRow, now time.Time) ownerLink {
	link := ownerLink{
		ID: row.LinkID, NotebookID: row.NotebookID, NotebookTitle: row.Title, NotebookIcon: row.Icon,
		ShareToken: row.ShareToken,
		PublicURL:  strings.TrimRight(r.FrontendURL, "/") + publicPagePathStem + row.ShareToken,
		CreatedAt:  row.CreatedAt.UTC(),
		IsExpired:  isExpired(row.ExpiresAt, now),
	}
	if row.ExpiresAt != nil {
		at := row.ExpiresAt.UTC()
		link.ExpiresAt = &at
	}
	return link
}

// isExpired says a link's time is up. A link with no expiry never is.
func isExpired(expiresAt *time.Time, now time.Time) bool {
	return expiresAt != nil && !expiresAt.After(now)
}

// newToken is a link's address: 48 bytes from the system's generator, as
// 64 URL-safe characters. It is the only thing between a notebook and a
// stranger, so it is as long as the Python's was.
func newToken() (string, error) {
	raw := make([]byte, 48)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// linkOf reads one of the caller's notebooks with its link. False means the
// answer has been written (the 404 for a notebook that is not theirs, or
// the 500). A nil link is a notebook that is not shared.
func (x *exchange) linkOf(notebookID int64) (*ownerLink, bool) {
	row, err := New(x.routes.Pool).LinkOfNotebook(x.ctx.Context(), LinkOfNotebookParams{NotebookID: notebookID, UserID: x.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, notebookAbsent)
		return nil, false
	}
	if err != nil {
		x.fail("could not read the notebook's link", err)
		return nil, false
	}
	if row.LinkID == nil || row.ShareToken == nil || row.CreatedAt == nil {
		return nil, true
	}
	link := x.routes.describe(LinksOfOwnerRow{
		NotebookID: row.NotebookID, Title: row.Title, Icon: row.Icon,
		LinkID: *row.LinkID, ShareToken: *row.ShareToken, CreatedAt: *row.CreatedAt, ExpiresAt: row.ExpiresAt,
	}, x.routes.now())
	return &link, true
}

func (r Routes) read(x *exchange) {
	notebookID, ok := x.id("notebook_id")
	if !ok {
		return
	}
	if link, ok := x.linkOf(notebookID); ok {
		x.send(http.StatusOK, ownerCache, struct {
			Link *ownerLink `json:"link"`
		}{link})
	}
}

// shareRequest is what Share asks for. The days are 7, 30 or null for a
// link that never expires; left out, they are 7.
type shareRequest struct {
	days       *int
	regenerate bool
}

// defaultExpiryDays is how long a link lives when the request does not say.
const defaultExpiryDays = 7

// readShareRequest reads the body, which may be absent. The failure, if
// any, names the field.
func readShareRequest(body io.Reader) (shareRequest, *errorenvelope.Failure) {
	seven := defaultExpiryDays
	asked := shareRequest{days: &seven}
	raw, err := io.ReadAll(io.LimitReader(body, maxBodyBytes+1))
	if err != nil || len(raw) > maxBodyBytes {
		return asked, &errorenvelope.Failure{Location: []string{"body"}, Message: "The body is too large"}
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return asked, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return asked, &errorenvelope.Failure{Location: []string{"body"}, Message: "The body must be a JSON object"}
	}
	if days, named := fields["expires_in_days"]; named {
		var value *int
		if json.Unmarshal(days, &value) != nil || (value != nil && *value != 7 && *value != 30) {
			return asked, &errorenvelope.Failure{Location: []string{"body", "expires_in_days"}, Message: "Input should be 7, 30 or null"}
		}
		asked.days = value
	}
	if regenerate, named := fields["regenerate"]; named {
		if json.Unmarshal(regenerate, &asked.regenerate) != nil {
			return asked, &errorenvelope.Failure{Location: []string{"body", "regenerate"}, Message: "Input should be a valid boolean"}
		}
	}
	return asked, nil
}

func (r Routes) share(x *exchange) {
	notebookID, ok := x.id("notebook_id")
	if !ok {
		return
	}
	asked, failure := readShareRequest(x.ctx.BodyReader())
	if failure != nil {
		r.Errors.WriteValidation(x.response, x.requestID, []errorenvelope.Failure{*failure})
		return
	}
	token, err := newToken()
	if err != nil {
		x.fail("could not make a link's token", err)
		return
	}
	now := r.now()
	var expiresAt *time.Time
	if asked.days != nil {
		at := now.AddDate(0, 0, *asked.days)
		expiresAt = &at
	}
	written, err := New(r.Pool).ShareNotebook(x.ctx.Context(), ShareNotebookParams{
		NewToken: token, ExpiresAt: expiresAt, Now: now,
		NotebookID: notebookID, UserID: x.userID, Regenerate: asked.regenerate,
	})
	if err != nil {
		x.fail("could not share the notebook", err)
		return
	}
	if written == 0 {
		x.refuse(http.StatusNotFound, notebookAbsent)
		return
	}
	link, ok := x.linkOf(notebookID)
	if !ok {
		return
	}
	if link == nil {
		// Stopped between the write and the read, by the same account in
		// another tab: there is no link to answer.
		x.refuse(http.StatusNotFound, linkAbsent)
		return
	}
	x.send(http.StatusOK, ownerCache, link)
}

func (r Routes) stop(x *exchange) {
	notebookID, ok := x.id("notebook_id")
	if !ok {
		return
	}
	removed, err := New(r.Pool).StopSharing(x.ctx.Context(), StopSharingParams{NotebookID: notebookID, UserID: x.userID})
	r.stopped(x, removed, err)
}

func (r Routes) revoke(x *exchange) {
	linkID, ok := x.id("link_id")
	if !ok {
		return
	}
	removed, err := New(r.Pool).RevokeLink(x.ctx.Context(), RevokeLinkParams{ID: linkID, UserID: x.userID})
	r.stopped(x, removed, err)
}

// stopped answers either removal. Nothing removed is a 404 whether the link
// never existed, was already stopped, or is another account's.
func (r Routes) stopped(x *exchange, removed int64, err error) {
	switch {
	case err != nil:
		x.fail("could not stop a public link", err)
	case removed == 0:
		x.refuse(http.StatusNotFound, linkAbsent)
	default:
		x.response.WriteHeader(http.StatusNoContent)
	}
}

func (r Routes) list(x *exchange) {
	rows, err := New(r.Pool).LinksOfOwner(x.ctx.Context(), x.userID)
	if err != nil {
		x.fail("could not list the account's public links", err)
		return
	}
	now := r.now()
	links := make([]ownerLink, 0, len(rows))
	for _, row := range rows {
		links = append(links, r.describe(row, now))
	}
	x.send(http.StatusOK, ownerCache, struct {
		Links []ownerLink `json:"links"`
	}{links})
}
