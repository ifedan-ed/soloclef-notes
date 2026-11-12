package notebooklinks

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// shared is a resolved link: the notebook it shows and the account that
// notebook belongs to. Every statement a visitor's request runs is handed
// both, from here and never from the request.
type shared struct {
	token      string
	notebookID int64
	link       LiveLinkRow
}

// resolve is the only way in for a visitor. The token must name a link
// that has not expired, of a notebook that still exists, of an account that
// is still active. It is read afresh on every request, so stopping or
// regenerating a link takes effect on the visitor's next click.
//
// Unknown, revoked and deleted are one answer, 404; expired is 410, which
// the page explains differently ("this link has expired, ask for a new
// one"). An expired link discloses only that it once existed, to someone
// who already held its token.
func (x *exchange) resolve() (shared, bool) {
	token := x.ctx.Param("share_token")
	if token == "" || len(token) > maxTokenChars {
		x.refuse(http.StatusNotFound, gone)
		return shared{}, false
	}
	link, err := New(x.routes.Pool).LiveLink(x.ctx.Context(), token)
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, gone)
		return shared{}, false
	}
	if err != nil {
		x.fail("could not resolve a public link", err)
		return shared{}, false
	}
	if isExpired(link.ExpiresAt, x.routes.now()) {
		x.refuse(http.StatusGone, expired)
		return shared{}, false
	}
	x.userID = link.UserID
	return shared{token: link.ShareToken, notebookID: link.NotebookID, link: link}, true
}

// sources is the documents the link shows (or the one with this id; 0 is
// all), in the order a visitor lists them.
func (x *exchange) sources(link shared, onlyID int64) ([]SharedSourcesRow, error) {
	return New(x.routes.Pool).SharedSources(x.ctx.Context(), SharedSourcesParams{
		NotebookID: link.notebookID, UserID: x.userID, OnlyID: onlyID})
}

// chat reads the notebook's shared chat and confirms what its cards made.
func (x *exchange) chat(link shared) ([]chatMessage, map[madeRef]madeArtifact, error) {
	ctx := x.ctx.Context()
	q := New(x.routes.Pool)
	rows, err := q.SharedChat(ctx, SharedChatParams{NotebookID: &link.notebookID, UserID: x.userID})
	if err != nil {
		return nil, nil, err
	}
	messages := make([]chatMessage, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, decodeMessage(row))
	}
	artifacts, podcasts, jobs := madeRefs(messages)
	made := map[madeRef]madeArtifact{}
	if len(artifacts)+len(podcasts)+len(jobs) == 0 {
		return messages, made, nil
	}
	confirmed, err := q.MadeArtifacts(ctx, MadeArtifactsParams{
		ArtifactIds: artifacts, PodcastIds: podcasts, JobIds: jobs, UserID: x.userID})
	if err != nil {
		return nil, nil, err
	}
	for _, row := range confirmed {
		made[madeRef{row.Kind, row.Ref}] = madeArtifact{id: row.ArtifactID, title: row.Title, format: row.Format}
	}
	return messages, made, nil
}

// showsArtifact says whether the link shows this artifact: one of the
// notebook's finished outputs, or something its shared chat made. The chat
// is read only when the cheaper question says no.
func (x *exchange) showsArtifact(link shared, artifactID int64) (bool, error) {
	if artifactID == 0 {
		return false, nil
	}
	isOutput, err := New(x.routes.Pool).IsOutputArtifact(x.ctx.Context(), IsOutputArtifactParams{
		NotebookID: link.notebookID, UserID: x.userID, ArtifactID: artifactID})
	if err != nil || isOutput {
		return isOutput, err
	}
	_, made, err := x.chat(link)
	if err != nil {
		return false, err
	}
	for _, artifact := range made {
		if artifact.id == artifactID {
			return true, nil
		}
	}
	return false, nil
}
