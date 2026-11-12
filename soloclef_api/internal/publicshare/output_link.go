package publicshare

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// This file makes the public link to one Studio output: a frozen copy of
// its title, kind, format and text, and the one artifact the link's file
// routes may serve. Nothing else about the notebook or its sources goes
// into it.
//
// Sharing an output again while its content is unchanged answers the same
// link, with the title it has now and the expiry just chosen. A changed
// output gets a new link, and the old one keeps its copy.

// defaultExpiryDays is how long a link lives when the request does not
// say: the share dialog's first choice.
const defaultExpiryDays = 7

// maxLinkBody bounds the request body, which is one small object.
const maxLinkBody = 4 << 10

// errOutputUnfinished says the output's run has not finished, or made no
// artifact: there is nothing to freeze yet.
var errOutputUnfinished = errors.New("publicshare: the output has no finished artifact")

// The frozen copy, in the shape the page route reads (internal/publicchat).
type (
	outputCopy struct {
		Kind        string       `json:"kind"`
		Title       string       `json:"title"`
		SnapshotAt  string       `json:"snapshot_at"`
		Output      sharedOutput `json:"output"`
		ArtifactIDs []int64      `json:"artifact_ids"`
	}
	sharedOutput struct {
		Title      string      `json:"title"`
		Kind       string      `json:"kind"`
		Format     string      `json:"format"`
		Markdown   string      `json:"markdown"`
		ArtifactID int64       `json:"artifact_id"`
		File       *sharedMain `json:"file"`
	}
	// sharedMain describes the main file for a page that predates the
	// link's file list; the list itself is read when the link is opened.
	sharedMain struct {
		Filename  string `json:"filename"`
		MimeType  string `json:"mime_type"`
		SizeBytes int64  `json:"size_bytes"`
		Display   string `json:"display"`
	}
	// createdLink is what the share dialog reads.
	createdLink struct {
		SnapshotID int64   `json:"snapshot_id"`
		ShareToken string  `json:"share_token"`
		PublicURL  string  `json:"public_url"`
		IsNew      bool    `json:"is_new"`
		ExpiresAt  *string `json:"expires_at"`
	}
)

// readExpiry reads `{"expires_in_days": 7 | 30 | null}`. No body and no
// field are the default; null is a link that never expires.
func readExpiry(body io.Reader) (days int, expires bool, ok bool) {
	raw, err := io.ReadAll(io.LimitReader(body, maxLinkBody+1))
	if err != nil || len(raw) > maxLinkBody {
		return 0, false, false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return defaultExpiryDays, true, true
	}
	var asked map[string]json.RawMessage
	if json.Unmarshal(raw, &asked) != nil {
		return 0, false, false
	}
	value, sent := asked["expires_in_days"]
	if !sent {
		return defaultExpiryDays, true, true
	}
	switch string(bytes.TrimSpace(value)) {
	case "null":
		return 0, false, true
	case "7":
		return 7, true, true
	case "30":
		return 30, true, true
	}
	return 0, false, false
}

// newToken is a link's address: 48 random bytes, as every share link's is.
func newToken() (string, error) {
	raw := make([]byte, 48)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("publicshare: no randomness for a link's token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// shareOutput makes, or renews, the link to one of the caller's finished
// outputs.
func (r Routes) shareOutput(x *exchange) {
	caller, ok := x.signedIn()
	if !ok {
		return
	}
	ids, ok := x.ids("notebook_id", "output_id")
	if !ok {
		return
	}
	days, expires, ok := readExpiry(x.ctx.BodyReader())
	if !ok {
		x.routes.Errors.WriteValidation(x.response, x.requestID, []errorenvelope.Failure{{
			Location: []string{"body", "expires_in_days"}, Message: badExpiry}})
		return
	}
	now := r.now()
	var expiresAt *time.Time
	if expires {
		at := now.Add(time.Duration(days) * 24 * time.Hour)
		expiresAt = &at
	}
	// Made before the transaction: it needs no row, and a transaction is
	// not the place to wait on the system's randomness.
	token, err := newToken()
	if err != nil {
		x.fail("could not make a link's token", err)
		return
	}

	link, err := database.Write(x.ctx.Context(), r.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (createdLink, error) {
		q := New(tx)
		output, err := q.OutputToShare(ctx, OutputToShareParams{OutputID: ids[1], NotebookID: ids[0], UserID: caller.UserID})
		if err != nil {
			return createdLink{}, err
		}
		if output.ArtifactID == nil || output.ArtifactFormat == nil || output.ArtifactGeneration == nil {
			return createdLink{}, errOutputUnfinished
		}
		// What "unchanged" means: the same artifact, on the same
		// generation, with the same text.
		sum := sha256.Sum256([]byte(strconv.FormatInt(*output.ArtifactID, 10) + ":" +
			strconv.FormatInt(*output.ArtifactGeneration, 10) + ":" + output.Markdown))
		hash := hex.EncodeToString(sum[:])

		if err := q.DropExpiredOutputLinks(ctx, DropExpiredOutputLinksParams{
			OutputID: &output.ID, UserID: caller.UserID, ContentHash: hash, Now: now}); err != nil {
			return createdLink{}, err
		}
		renewed, err := q.RenewOutputLink(ctx, RenewOutputLinkParams{
			ExpiresAt: expiresAt, Title: output.Title, OutputID: &output.ID, UserID: caller.UserID, ContentHash: hash})
		if err == nil {
			return r.created(renewed.ID, renewed.ShareToken, renewed.ExpiresAt, false), nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return createdLink{}, err
		}

		shared := sharedOutput{Title: output.Title, Kind: output.Kind, Format: *output.ArtifactFormat,
			Markdown: output.Markdown, ArtifactID: *output.ArtifactID}
		main, err := q.PrimaryFileSummary(ctx, PrimaryFileSummaryParams{ArtifactID: *output.ArtifactID, UserID: caller.UserID})
		switch {
		case err == nil:
			shared.File = &sharedMain{Filename: main.OriginalFilename, MimeType: main.MimeType,
				SizeBytes: main.SizeBytes, Display: displayFor(main.MimeType)}
		case !errors.Is(err, pgx.ErrNoRows):
			// No row is an output that is only its text, such as a report.
			return createdLink{}, err
		}
		data, err := json.Marshal(outputCopy{Kind: "output", Title: output.Title, SnapshotAt: pythonjson.ISOFormat(now),
			Output: shared, ArtifactIDs: []int64{*output.ArtifactID}})
		if err != nil {
			return createdLink{}, err
		}
		inserted, err := q.InsertOutputLink(ctx, InsertOutputLinkParams{UserID: caller.UserID, OutputID: &output.ID,
			ShareToken: token, ContentHash: hash, SnapshotData: data, ExpiresAt: expiresAt})
		if err != nil {
			return createdLink{}, err
		}
		return r.created(inserted.ID, inserted.ShareToken, inserted.ExpiresAt, true), nil
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Not there, in another notebook and another account's are one
		// answer, so the pair of numbers tells nothing.
		x.refuse(http.StatusNotFound, outputAbsent)
	case errors.Is(err, errOutputUnfinished):
		x.refuse(http.StatusBadRequest, outputUnfinished)
	case err != nil:
		x.fail("could not make an output's public link", err)
	default:
		x.send(http.StatusOK, link)
	}
}

// created is the answer for a link.
func (r Routes) created(id int64, token string, expiresAt *time.Time, isNew bool) createdLink {
	link := createdLink{SnapshotID: id, ShareToken: token, PublicURL: r.publicURL(token), IsNew: isNew}
	if expiresAt != nil {
		at := pythonjson.PydanticDateTime(*expiresAt)
		link.ExpiresAt = &at
	}
	return link
}
