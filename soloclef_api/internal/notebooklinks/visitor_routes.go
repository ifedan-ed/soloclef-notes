package notebooklinks

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentfiles"
)

// visitorJSON is a visitor's JSON answer: never stored, so a stopped link
// is not read out of a cache.
const visitorJSON = "no-store"

// fallbackType is what a file whose row names no type is described as:
// bytes, to be saved and not interpreted.
const fallbackType = "application/octet-stream"

type (
	liveNotebook struct {
		Notebook  liveTitle               `json:"notebook"`
		ExpiresAt *time.Time              `json:"expires_at"`
		Messages  []liveMessage           `json:"messages"`
		Citations map[string]liveCitation `json:"citations"`
		Outputs   []liveOutput            `json:"outputs"`
		Sources   []liveSource            `json:"sources"`
	}
	liveTitle struct {
		Title string  `json:"title"`
		Icon  *string `json:"icon"`
	}
	liveOutput struct {
		ID              int64     `json:"id"`
		Kind            string    `json:"kind"`
		Title           string    `json:"title"`
		Format          string    `json:"format"`
		ArtifactID      int64     `json:"artifact_id"`
		DurationSeconds *int64    `json:"duration_seconds"`
		CreatedAt       time.Time `json:"created_at"`
	}
	liveSource struct {
		ID           int64  `json:"id"`
		Title        string `json:"title"`
		DocumentType string `json:"document_type"`
		HasFile      bool   `json:"has_file"`
	}
	// liveFile is a file as a viewer fetches it: what it is and this link's
	// address for its bytes. Where it is stored is not sent.
	liveFile struct {
		FileID     int64  `json:"file_id"`
		Role       string `json:"role,omitempty"`
		Filename   string `json:"filename"`
		MimeType   string `json:"mime_type"`
		SizeBytes  int64  `json:"size_bytes"`
		ContentURL string `json:"content_url"`
	}
)

// page is the whole notebook as it is now: its title, the shared chat, the
// finished outputs and the source list.
func (r Routes) page(x *exchange, link shared) {
	ctx := x.ctx.Context()
	q := New(r.Pool)
	documents, err := x.sources(link, 0)
	if err != nil {
		x.fail("could not read a shared notebook's sources", err)
		return
	}
	answer := liveNotebook{
		Notebook: liveTitle{Title: link.link.Title, Icon: link.link.Icon},
		Sources:  make([]liveSource, 0, len(documents)),
		Outputs:  []liveOutput{},
	}
	if link.link.ExpiresAt != nil {
		at := link.link.ExpiresAt.UTC()
		answer.ExpiresAt = &at
	}
	titles := make(map[int64]string, len(documents))
	documentIDs := make([]int64, 0, len(documents))
	for _, document := range documents {
		answer.Sources = append(answer.Sources, liveSource(document))
		titles[document.ID] = document.Title
		documentIDs = append(documentIDs, document.ID)
	}

	messages, made, err := x.chat(link)
	if err != nil {
		x.fail("could not read a shared notebook's chat", err)
		return
	}
	places := map[int64]passagePlace{}
	if cited := citedIDs(messages); len(cited) > 0 && len(documentIDs) > 0 {
		passages, err := q.CitedPassages(ctx, CitedPassagesParams{ChunkIds: cited, UserID: x.userID, DocumentIds: documentIDs})
		if err != nil {
			x.fail("could not place a shared chat's citations", err)
			return
		}
		for _, passage := range passages {
			places[passage.ID] = passagePlace{documentID: passage.DocumentID, page: passage.PageStart}
		}
	}
	answer.Messages, answer.Citations = publicChat(messages, places, titles, made)

	outputs, err := q.SharedOutputs(ctx, SharedOutputsParams{NotebookID: link.notebookID, UserID: x.userID})
	if err != nil {
		x.fail("could not read a shared notebook's outputs", err)
		return
	}
	for _, output := range outputs {
		duration := output.PodcastDuration
		if duration == nil {
			duration = recordedDuration(output.Metadata)
		}
		answer.Outputs = append(answer.Outputs, liveOutput{
			ID: output.ID, Kind: output.Kind, Title: output.Title, Format: output.Format,
			ArtifactID: output.ArtifactID, DurationSeconds: duration, CreatedAt: output.CreatedAt.UTC(),
		})
	}
	x.send(http.StatusOK, visitorJSON, answer)
}

// recordedDuration is the whole seconds a render recorded in the artifact's
// metadata, or nothing when it recorded none that is a positive number
// (the rule the owner's studio list shows a length by).
func recordedDuration(metadata []byte) *int64 {
	var fields map[string]any
	if json.Unmarshal(metadata, &fields) != nil {
		return nil
	}
	seconds, ok := fields["duration_seconds"].(float64)
	if !ok || seconds <= 0 {
		return nil
	}
	whole := int64(math.Round(seconds))
	return &whole
}

// artifact reads an artifact the link shows, with its files. False means
// the answer has been written.
func (x *exchange) artifact(link shared) (ArtifactShownRow, []ArtifactFilesRow, bool) {
	artifactID := x.visitorID("artifact_id")
	shown, err := x.showsArtifact(link, artifactID)
	if err != nil {
		x.fail("could not decide whether a link shows an artifact", err)
		return ArtifactShownRow{}, nil, false
	}
	if !shown {
		x.refuse(http.StatusNotFound, gone)
		return ArtifactShownRow{}, nil, false
	}
	q := New(x.routes.Pool)
	row, err := q.ArtifactShown(x.ctx.Context(), ArtifactShownParams{ArtifactID: artifactID, UserID: x.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, gone)
		return ArtifactShownRow{}, nil, false
	}
	if err != nil {
		x.fail("could not read a shared artifact", err)
		return ArtifactShownRow{}, nil, false
	}
	files, err := q.ArtifactFiles(x.ctx.Context(), ArtifactFilesParams{ArtifactID: artifactID, UserID: x.userID})
	if err != nil {
		x.fail("could not read a shared artifact's files", err)
		return ArtifactShownRow{}, nil, false
	}
	return row, files, true
}

// manifest is the in-app manifest's shape, so the in-app viewers render it,
// with this link's file addresses. It carries no study state (a visitor's
// answers stay in their browser) and no document id: the field is there
// because the viewers' schema asks for a number, and 0 names nothing.
func (r Routes) manifest(x *exchange, link shared) {
	row, files, ok := x.artifact(link)
	if !ok {
		return
	}
	listed := make([]liveFile, 0, len(files))
	for _, file := range files {
		listed = append(listed, liveFile{
			FileID: file.ID, Role: file.Role, Filename: file.OriginalFilename,
			MimeType: typeOr(file.MimeType), SizeBytes: file.SizeBytes,
			ContentURL: publicBase + "/" + link.token + "/artifacts/" + strconv.FormatInt(row.ID, 10) +
				"/files/" + strconv.FormatInt(file.ID, 10) + "/content",
		})
	}
	x.send(http.StatusOK, visitorJSON, struct {
		ArtifactID int64      `json:"artifact_id"`
		DocumentID int64      `json:"document_id"`
		Title      string     `json:"title"`
		Format     string     `json:"format"`
		Generation int64      `json:"generation"`
		Markdown   string     `json:"markdown_representation"`
		Files      []liveFile `json:"files"`
		UpdatedAt  time.Time  `json:"updated_at"`
	}{row.ID, 0, row.Title, row.Format, row.Generation, row.Markdown, listed, row.UpdatedAt.UTC()})
}

// source is how a source opens: as its original file when the app has a
// viewer for it, else as its text, with the original (if any) to download.
// The text is not sent when the file is what will be shown.
func (r Routes) source(x *exchange, link shared) {
	documentID := x.visitorID("document_id")
	if documentID == 0 {
		x.refuse(http.StatusNotFound, gone)
		return
	}
	documents, err := x.sources(link, documentID)
	if err != nil {
		x.fail("could not look for a shared source", err)
		return
	}
	if len(documents) == 0 {
		x.refuse(http.StatusNotFound, gone)
		return
	}
	document := documents[0]
	row, err := New(r.Pool).SourceText(x.ctx.Context(), SourceTextParams{DocumentID: document.ID, UserID: x.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, gone)
		return
	}
	if err != nil {
		x.fail("could not read a shared source", err)
		return
	}
	answer := struct {
		ID           int64     `json:"id"`
		Title        string    `json:"title"`
		DocumentType string    `json:"document_type"`
		Presentation string    `json:"presentation"`
		File         *liveFile `json:"file"`
		Markdown     string    `json:"markdown"`
	}{ID: document.ID, Title: document.Title, DocumentType: document.DocumentType, Presentation: "text", Markdown: row.Markdown}
	if row.FileID != 0 {
		viewed := documentfiles.ViewedType(row.OriginalFilename, row.MimeType)
		mediaType := viewed
		if mediaType == "" {
			mediaType = typeOr(row.MimeType)
		}
		answer.File = &liveFile{
			FileID: row.FileID, Filename: row.OriginalFilename, MimeType: mediaType, SizeBytes: row.SizeBytes,
			ContentURL: publicBase + "/" + link.token + "/sources/" + strconv.FormatInt(document.ID, 10) +
				"/files/" + strconv.FormatInt(row.FileID, 10) + "/content",
		}
		if viewed != "" {
			answer.Presentation, answer.Markdown = "original", ""
		}
	}
	x.send(http.StatusOK, visitorJSON, answer)
}

// typeOr is a stored media type, or plain bytes when the row names none.
func typeOr(stored string) string {
	if stored == "" {
		return fallbackType
	}
	return stored
}
