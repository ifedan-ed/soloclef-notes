package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_filestore/objecthttp"
)

// Manifests reads what the manifest describes.
type Manifests interface {
	ManifestOf(ctx context.Context, userID string, artifactID int64) (*ManifestRow, error)
}

// ManifestRoute serves an artifact's manifest: what the Studio's panel reads
// to decide how to show an artifact -- its title and format, its Markdown,
// the files it has and where to fetch each, and, for a deck or a quiz, the
// caller's own study state.
//
// The web app refetches it every time its window regains focus, so it is the
// most-called route of this package.
type ManifestRoute struct {
	Gate
	Manifests Manifests
	// StudyFiles reads a deck or a quiz for its size, and remembers it
	// (study_cache.go): this route is why remembering pays.
	StudyFiles *StudyFiles
}

func (r ManifestRoute) Register(api huma.API) {
	register(api, "read-artifact-manifest", http.MethodGet, ManifestPath,
		"What an artifact is, its files, and the caller's study state", r.serve)
}

func (r ManifestRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	request, ok := r.admit(ctx, response, nil)
	if !ok {
		return
	}
	row, err := r.Manifests.ManifestOf(ctx.Context(), request.caller.UserID, request.artifactID)
	if err != nil {
		r.fail(ctx.Context(), response, request.requestID, "could not read the artifact", err)
		return
	}
	if row == nil {
		r.Errors.Write(response, http.StatusNotFound, artifactAbsent, request.requestID)
		return
	}

	manifest := buildManifest(*row)
	// The two formats with study state. Only they have the file read.
	switch row.Format {
	case flashcardsFormat:
		stored := decodeColumn(row.FlashcardState)
		cards, err := r.StudyFiles.Deck(ctx.Context(), primaryOf(row.Files))
		if err != nil {
			r.studyFileUnread(ctx.Context(), request, err)
			cards = deckSizeIn(stored)
		}
		state, studying := readFlashcardState(stored, row.Generation, cards)
		if !studying {
			state = newFlashcardState(row.Generation, cards)
		}
		manifest.Set("flashcard_study_state", state.render())
	case quizFormat:
		stored := decodeColumn(row.QuizState)
		questions := 0
		correct, err := r.StudyFiles.Quiz(ctx.Context(), primaryOf(row.Files))
		if err == nil {
			questions = len(correct)
		} else {
			r.studyFileUnread(ctx.Context(), request, err)
			questions = quizSizeIn(stored)
		}
		state, started := readQuizState(stored, row.Generation, questions)
		if !started {
			state = newQuizState(row.Generation, questions)
		}
		manifest.Set("quiz_state", state.render())
	}

	body, err := pythonjson.Marshal(manifest)
	if err != nil {
		r.fail(ctx.Context(), response, request.requestID, "could not render the manifest", err)
		return
	}

	// The validator is a hash of the bytes about to be sent. Whatever the
	// manifest says -- a title, a file, a mark on a card -- cannot change
	// without the tag changing, and there is no list of "things the tag
	// covers" to forget to extend. 128 bits of SHA-256 is far more than a
	// cache key needs.
	//
	// `no-cache` means "keep it, but ask before using it": the answer is
	// per-person and changes with every card flipped, and a 304 is how
	// asking stays cheap.
	digest := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(digest[:16]) + `"`
	response.Header().Set("ETag", etag)
	response.Header().Set("Cache-Control", revalidate)
	if objecthttp.NoneMatch(ctx.Header("If-None-Match"), etag) {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// studyFileUnread records a manifest whose study file could not be read.
//
// The manifest is still answered. It is how the panel learns the artifact's
// title and where its file is, and the file can still be downloaded even if
// it cannot be studied; refusing the manifest would leave a deck that can be
// neither opened nor fetched nor deleted from the panel.
//
// What it must not do is tell the viewer that the person's progress is gone,
// and it used to. A stored state is checked against the deck -- so many
// cards, so many questions -- and with no deck to count, it was checked
// against zero, failed, and was replaced by the state of somebody who had
// never started. The read fails for a moment (a store that hiccups) far more
// often than for good, the viewer fetches the file itself and usually gets
// it, and it then showed a half-studied deck as untouched while the progress
// sat intact in the row.
//
// So when the file cannot be counted, the stored state is checked against the
// size it implies itself (deckSizeIn, quizSizeIn) and sent if it is coherent
// at that size. Nothing is taken on trust that was checked before: the web
// viewer validates the state against the file it loaded, as it always has
// (normalizeFlashcardStudyState, normalizeQuizState), and falls back to a
// fresh one if they disagree -- which is no worse than what it was sent
// before. The shape on the wire is unchanged, so no client needed to learn a
// new one; the phone app does not read study state from the manifest at all.
//
// With nothing stored, the state is still one over zero cards, which the
// viewer replaces with its own count once it has loaded the file.
func (r ManifestRoute) studyFileUnread(ctx context.Context, request admitted, cause error) {
	r.Logger.LogAttrs(ctx, slog.LevelWarn, "an artifact's study file could not be read for its manifest",
		slog.String("request_id", request.requestID),
		slog.Int64("artifact_id", request.artifactID),
		slog.String("error", cause.Error()))
}

// buildManifest is everything in the manifest that comes from rows, in the
// field names the web app parses (ArtifactManifestSchema).
func buildManifest(row ManifestRow) *pythonjson.Object {
	manifest := pythonjson.NewObject()
	manifest.Set("artifact_id", row.ID)
	manifest.Set("document_id", row.DocumentID)
	manifest.Set("title", row.Title)
	manifest.Set("format", row.Format)
	manifest.Set("generation", row.Generation)
	manifest.Set("markdown_representation", row.Markdown)
	files := make([]any, 0, len(row.Files))
	for _, file := range row.Files {
		files = append(files, manifestFile(row.ID, file))
	}
	manifest.Set("files", files)
	manifest.Set("updated_at", pythonjson.ISOFormat(row.UpdatedAt))
	// Absent, not null, when there is no legacy reference.
	if legacy := legacyFrom(decodeColumn(row.Legacy)); legacy != nil {
		manifest.Set("legacy", legacy)
	}
	return manifest
}

// manifestFile is one file as the panel needs it: what it is, and the URL
// that serves its bytes (FileContentRoute). Where it is *stored* -- the
// backend, the key, the checksum -- is not the panel's business and is not
// sent.
func manifestFile(artifactID int64, file StoredFile) *pythonjson.Object {
	entry := pythonjson.NewObject()
	entry.Set("file_id", file.ID)
	entry.Set("role", file.Role)
	entry.Set("filename", file.Filename)
	entry.Set("mime_type", typeOf(file.MimeType))
	entry.Set("size_bytes", file.Size)
	entry.Set("content_url",
		"/api/v1/artifacts/"+strconv.FormatInt(artifactID, 10)+
			"/files/"+strconv.FormatInt(file.ID, 10)+"/content")
	return entry
}

// primaryOf is the file that is the artifact, or nil.
func primaryOf(files []StoredFile) *StoredFile {
	for index := range files {
		if files[index].Role == primaryRole {
			return &files[index]
		}
	}
	return nil
}
