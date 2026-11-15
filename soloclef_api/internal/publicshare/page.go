package publicshare

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// This file is the page a public link opens: the shared output as it was
// frozen (its title, kind, format and text) with its artifact's files as
// they are now, each at this link's own address.

// fallbackMimeType is what a file whose row names no type is listed as, so
// a viewer treats it as bytes to save rather than guessing.
const fallbackMimeType = "application/octet-stream"

type (
	// page is the answer's shape. `kind`, `thread` and `messages` are left
	// from when a link could also share a chat: every installed web and
	// phone build parses them and the phone requires `thread` and
	// `messages`, so they are sent, fixed, until those builds are gone. Only
	// `output` and `expires_at` carry anything.
	page struct {
		Kind      string     `json:"kind"`
		Thread    pageTitle  `json:"thread"`
		Messages  []struct{} `json:"messages"`
		Output    pageOutput `json:"output"`
		ExpiresAt *string    `json:"expires_at"`
	}
	// pageTitle is what was shared and when the link was made.
	pageTitle struct {
		Title     string `json:"title"`
		CreatedAt string `json:"created_at"`
	}
	pageOutput struct {
		Title      string      `json:"title"`
		Kind       string      `json:"kind"`
		Format     string      `json:"format"`
		Markdown   string      `json:"markdown"`
		ArtifactID int64       `json:"artifact_id"`
		File       *sharedMain `json:"file"`
		// Generation is the artifact's, absent when it is gone: there is
		// no generation to name, and every viewer reads absent as the first.
		Generation *int64     `json:"generation,omitempty"`
		Files      []pageFile `json:"files"`
	}
	// pageFile is one file in the in-app manifest's shape, so the owner's
	// viewers read a shared output exactly as they read their own.
	pageFile struct {
		FileID     int64  `json:"file_id"`
		Role       string `json:"role"`
		Filename   string `json:"filename"`
		MimeType   string `json:"mime_type"`
		SizeBytes  int64  `json:"size_bytes"`
		ContentURL string `json:"content_url"`
	}
)

// page answers what the link shows.
func (r Routes) page(x *exchange) {
	token := x.ctx.Param("share_token")
	q := New(r.Pool)
	link, err := q.PageLink(x.ctx.Context(), token)
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, pageAbsent)
		return
	}
	if err != nil {
		x.fail("could not read a public link", err)
		return
	}
	if link.ExpiresAt != nil && !link.ExpiresAt.After(r.now()) {
		// `<=`: a link is already expired at the instant it expires, as the
		// file statements count it.
		x.refuse(http.StatusGone, pageExpired)
		return
	}

	var frozen outputCopy
	if err := json.Unmarshal(link.SnapshotData, &frozen); err != nil {
		x.fail("a link's frozen copy could not be read", err)
		return
	}
	shared := frozen.Output
	output := pageOutput{Title: shared.Title, Kind: shared.Kind, Format: shared.Format,
		Markdown: withoutCitations(shared.Markdown), ArtifactID: shared.ArtifactID, File: shared.File, Files: []pageFile{}}

	artifact, err := q.SharedArtifactFiles(x.ctx.Context(), SharedArtifactFilesParams{
		ShareToken: token, Now: r.now(), ArtifactID: shared.ArtifactID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Deleted since it was shared, or not the link owner's: the frozen
		// text is all there is, and no files are listed.
	case err != nil:
		x.fail("could not read a shared artifact's files", err)
		return
	default:
		files, err := r.listedFiles(token, shared.ArtifactID, artifact.Files)
		if err != nil {
			x.fail("a shared artifact's files could not be read", err)
			return
		}
		output.Generation, output.Files = &artifact.Generation, files
	}

	title := frozen.Title
	if title == "" {
		title = shared.Title
	}
	answer := page{Kind: "output", Thread: pageTitle{Title: title, CreatedAt: pythonjson.PydanticDateTime(link.CreatedAt)},
		Messages: []struct{}{}, Output: output}
	if link.ExpiresAt != nil {
		at := pythonjson.PydanticDateTime(*link.ExpiresAt)
		answer.ExpiresAt = &at
	}
	x.send(http.StatusOK, answer)
}

// listedFiles gives each stored file the address this link serves it at.
func (r Routes) listedFiles(token string, artifactID int64, stored []byte) ([]pageFile, error) {
	var files []pageFile
	if err := json.Unmarshal(stored, &files); err != nil {
		return nil, err
	}
	prefix := "/api/v1/public/" + url.PathEscape(token) + "/artifacts/" + strconv.FormatInt(artifactID, 10) + "/files/"
	for i := range files {
		if files[i].MimeType == "" {
			files[i].MimeType = fallbackMimeType
		}
		files[i].ContentURL = prefix + strconv.FormatInt(files[i].FileID, 10) + "/content"
	}
	if files == nil {
		files = []pageFile{}
	}
	return files, nil
}
