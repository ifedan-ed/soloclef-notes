package medialinks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_filestore/objecthttp"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/artifacts"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Routes is the two routes.
type Routes struct {
	Pool  *pgxpool.Pool
	Files *filestore.Stores
	// Secret signs the links. It is the deployment's session secret; the
	// signature's own domain keeps a link from being anything else.
	Secret string
	// StallTimeout is how long a player may accept nothing before it is
	// dropped: a limit on stalling, not on the length of the recording.
	StallTimeout time.Duration
	Errors       errorenvelope.Writer
	Logger       *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

// Check reports what is missing. Called once at start-up: a deployment
// with no secret would sign links anyone could forge.
func (r Routes) Check() error {
	switch {
	case r.Pool == nil:
		return errors.New("medialinks: the database pool is not set")
	case r.Files == nil:
		return errors.New("medialinks: the file stores are not set")
	case r.Secret == "":
		return errors.New("medialinks: the signing secret is not set")
	case r.StallTimeout <= 0:
		return errors.New("medialinks: the stall timeout is not set")
	case r.Logger == nil:
		return errors.New("medialinks: the logger is not set")
	}
	return nil
}

func (r Routes) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

type tokenPath struct {
	Token string `path:"token"`
}

// Register mounts the routes.
func (r Routes) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "issue-artifact-media-links", Method: http.MethodPost, Path: IssuePath,
		Summary: "Signed links for the caller's audio and video files", Tags: []string{"artifacts"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.issue}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "play-artifact-media", Method: http.MethodGet, Path: MediaPath,
		Summary: "The media file a signed link names", Tags: []string{"artifacts"},
	}, func(context.Context, *tokenPath) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.play}, nil
	})
}

func (r Routes) fail(ctx context.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	if !errors.Is(cause, context.Canceled) {
		attributes := []any{slog.String("request_id", requestID), slog.String("reason", reason)}
		if cause != nil {
			attributes = append(attributes, slog.String("error", cause.Error()))
		}
		r.Logger.ErrorContext(ctx, "a media link route could not be served", attributes...)
	}
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}

// link is one link as the viewer reads it.
type link struct {
	ArtifactID int64 `json:"artifact_id"`
	FileID     int64 `json:"file_id"`
	// URL is a path on this API; the viewer makes it absolute.
	URL       string    `json:"url"`
	MimeType  string    `json:"mime_type"`
	ExpiresAt time.Time `json:"expires_at"`
}

// issue answers a link for each file named that is the caller's own audio
// or video. A file that is not is left out, not refused: the report still
// opens, with that one player saying its recording is unavailable.
func (r Routes) issue(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		r.fail(ctx.Context(), response, requestID, "the route ran without a caller", nil)
		return
	}
	// A link lets whoever holds it play the file with no further sign-in.
	// Only a person in their own session may mint one; an API key or an
	// agent's token reads files through the artifact routes, as itself.
	if !caller.IsSession() {
		r.Errors.Write(response, http.StatusForbidden, sessionRequired, requestID)
		return
	}
	var asked struct {
		Files []struct {
			ArtifactID int64 `json:"artifact_id"`
			FileID     int64 `json:"file_id"`
		} `json:"files"`
	}
	raw, err := io.ReadAll(io.LimitReader(ctx.BodyReader(), maxBodyBytes+1))
	if err != nil || len(raw) > maxBodyBytes || json.Unmarshal(raw, &asked) != nil ||
		len(asked.Files) == 0 || len(asked.Files) > maxLinks {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{{
			Location: []string{"body", "files"}, Message: "Name between 1 and 24 files, each as {artifact_id, file_id}"}})
		return
	}
	type pair struct{ artifact, file int64 }
	wanted := map[pair]bool{}
	fileIDs := make([]int64, 0, len(asked.Files))
	for _, file := range asked.Files {
		if file.ArtifactID <= 0 || file.FileID <= 0 {
			r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{{
				Location: []string{"body", "files"}, Message: "artifact_id and file_id must both be greater than 0"}})
			return
		}
		wanted[pair{file.ArtifactID, file.FileID}] = true
		fileIDs = append(fileIDs, file.FileID)
	}
	rows, err := New(r.Pool).MediaFilesOfOwner(ctx.Context(), MediaFilesOfOwnerParams{UserID: caller.UserID, FileIds: fileIDs})
	if err != nil {
		r.fail(ctx.Context(), response, requestID, "could not read the media files", err)
		return
	}
	expires := r.now().Add(linkLifetime).Truncate(time.Second).UTC()
	links := []link{}
	for _, row := range rows {
		// The file must be the artifact's it was named with: a claim binds
		// both, and a pair that does not exist is not signed.
		if !wanted[pair{row.ArtifactID, row.ID}] {
			continue
		}
		token, err := issue(claim{UserID: caller.UserID, ArtifactID: row.ArtifactID, FileID: row.ID, Expires: expires.Unix()}, r.Secret)
		if err != nil {
			r.fail(ctx.Context(), response, requestID, "could not sign a media link", err)
			return
		}
		links = append(links, link{ArtifactID: row.ArtifactID, FileID: row.ID, URL: mediaPrefix + token, MimeType: row.MimeType, ExpiresAt: expires})
	}
	response.Header().Set("Content-Type", "application/json")
	// The answer is credentials; nothing may keep it.
	response.Header().Set("Cache-Control", "private, no-store")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(struct {
		Links []link `json:"links"`
	}{links})
}

// play sends the file a link names. Every failure is the same 404, so a
// link reveals nothing it does not grant: not whether the file exists, not
// whether the link was once good.
func (r Routes) play(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	granted, ok := read(ctx.Param("token"), r.Secret, r.now())
	if !ok {
		r.Errors.Write(response, http.StatusNotFound, mediaAbsent, requestID)
		return
	}
	file, err := New(r.Pool).LinkedMedia(ctx.Context(), LinkedMediaParams{
		FileID: granted.FileID, ArtifactID: granted.ArtifactID, UserID: granted.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		r.Errors.Write(response, http.StatusNotFound, mediaAbsent, requestID)
		return
	}
	if err != nil {
		r.fail(ctx.Context(), response, requestID, "could not read the linked media file", err)
		return
	}
	// The address is the credential: it must not travel on in a Referer
	// header if the report links anywhere.
	response.Header().Set("Referrer-Policy", "no-referrer")
	tag := ""
	if file.ChecksumSha256 != "" {
		tag = `"` + file.ChecksumSha256 + `"`
	}
	artifacts.Gate{Errors: r.Errors, Logger: r.Logger}.StreamStored(ctx, response, requestID, r.Files, r.StallTimeout,
		artifacts.StoredObject{
			Backend: file.StorageBackend, Key: file.StorageKey, Absent: mediaAbsent,
			Headers: objecthttp.Headers{
				ContentType: file.MimeType,
				ETag:        tag,
				// A player seeks with Range requests against the same
				// address; the browser may keep what it fetched for the
				// link's own lifetime, and no shared cache may keep it.
				CacheControl: "private, max-age=3600",
				Disposition:  artifacts.ContentDisposition(file.OriginalFilename, true),
			},
		})
}
