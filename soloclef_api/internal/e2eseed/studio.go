package e2eseed

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// removeTimeout bounds taking back the files of a seed whose rows did not
// commit.
const removeTimeout = 10 * time.Second

// newID is a fresh random name: 32 hex digits, as every stored file has.
func newID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// saveArtifact writes a finished artifact the way the product does: the
// document it stands on, the artifact, and each file stored under
// artifacts/{account}/{artifact}/{role}/{random}{ext} with its row. It
// runs inside the caller's transaction and appends every key it stores to
// stored, so the caller can remove them if the transaction fails.
func (r Routes) saveArtifact(ctx context.Context, q *Queries, userID, title string, seed seeded, metadata []byte, stored *[]string) (int64, error) {
	hash, err := newID()
	if err != nil {
		return 0, err
	}
	documentID, err := q.SeedArtifactDocument(ctx, SeedArtifactDocumentParams{
		UserID: userID, Title: title, Markdown: seed.markdown, ContentHash: hash})
	if err != nil {
		return 0, err
	}
	artifactID, err := q.SeedArtifact(ctx, SeedArtifactParams{
		DocumentID: documentID, UserID: userID, Format: seed.format, ToolCallID: "e2e-seed-" + hash, Metadata: metadata})
	if err != nil {
		return 0, err
	}
	if err := q.LinkDocumentToArtifact(ctx, LinkDocumentToArtifactParams{ArtifactID: artifactID, DocumentID: documentID, UserID: userID}); err != nil {
		return 0, err
	}
	for _, file := range seed.files {
		name, err := newID()
		if err != nil {
			return 0, err
		}
		key := "artifacts/" + userID + "/" + strconv.FormatInt(artifactID, 10) + "/" + file.role + "/" + name + path.Ext(file.filename)
		if err := r.Files.Put(ctx, key, file.data, file.mimeType); err != nil {
			return 0, err
		}
		*stored = append(*stored, key)
		sum := sha256.Sum256(file.data)
		if err := q.SeedArtifactFile(ctx, SeedArtifactFileParams{
			ArtifactID: artifactID, Role: ArtifactFileRole(file.role), StorageBackend: r.Files.Name(), StorageKey: key,
			OriginalFilename: file.filename, MimeType: file.mimeType, SizeBytes: int64(len(file.data)),
			Checksum: hex.EncodeToString(sum[:]),
		}); err != nil {
			return 0, err
		}
	}
	return artifactID, nil
}

// inTransaction runs a seed and, if it fails, removes whatever files it
// had stored: a file no row points at would stay in the store for ever.
func (r Routes) inTransaction(ctx context.Context, seed func(q *Queries, stored *[]string) error) error {
	var stored []string
	err := pgx.BeginFunc(ctx, r.Pool, func(tx pgx.Tx) error { return seed(New(tx), &stored) })
	if err != nil && len(stored) > 0 {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), removeTimeout)
		defer cancel()
		for _, key := range stored {
			_ = r.Files.Remove(cleanup, key)
		}
	}
	return err
}

// The options the studio's dialogs would have recorded for each kind, so
// a seeded output's card reads as a real one's does.
var seedOptions = map[string]string{
	"audio":       `{"format": "deep_dive", "length": "default"}`,
	"video":       `{"format": "explainer"}`,
	"slides":      `{"format": "detailed", "length": "default"}`,
	"infographic": `{"style": "auto"}`,
	"quiz":        `{"count": "fewer", "focus": "E2E focus: the seeded questions"}`,
}

type outputAnswer struct {
	OutputID   int64  `json:"output_id"`
	ArtifactID *int64 `json:"artifact_id"`
}

// seedOutput makes a studio output in the state a test wants to look at:
// finished with its artifact, still running, or failed. Audio is a
// podcast run and everything else a deliverable job, as in the product,
// so the studio list derives each status exactly as it does live.
func (r Routes) seedOutput(x *exchange) {
	var asked struct {
		NotebookID int64  `json:"notebook_id"`
		Kind       string `json:"kind"`
		Status     string `json:"status"`
		Title      string `json:"title"`
		// A report's Markdown (what the reading view shows) and an
		// interactive report's page; each has a stub by default.
		Markdown string `json:"markdown"`
		HTML     string `json:"html"`
	}
	if !x.body(&asked) {
		return
	}
	if asked.Status == "" {
		asked.Status = "ready"
	}
	title := asked.Title
	if title == "" {
		title = "E2E " + strings.ReplaceAll(asked.Kind, "_", " ")
	}
	ctx := x.ctx.Context()
	now := r.now()
	var answer outputAnswer
	err := r.inTransaction(ctx, func(q *Queries, stored *[]string) error {
		if asked.Status != "ready" && asked.Status != "running" && asked.Status != "failed" {
			return errInvalid("status must be ready, running or failed")
		}
		seed, err := filesFor(asked.Kind, title)
		if err != nil {
			return err
		}
		if asked.Markdown != "" && seed.studioKind == "report" {
			seed.markdown = asked.Markdown
		}
		if asked.HTML != "" && asked.Kind == "report_html" {
			seed.files[0].data = []byte(asked.HTML)
		}
		userID, err := q.NotebookOwner(ctx, asked.NotebookID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errAbsent("notebook not found")
		}
		if err != nil {
			return err
		}
		sources, err := q.NotebookSourceDocuments(ctx, NotebookSourceDocumentsParams{NotebookID: asked.NotebookID, UserID: userID})
		if err != nil {
			return err
		}
		ready, running, failed := asked.Status == "ready", asked.Status == "running", asked.Status == "failed"
		if ready {
			var metadata []byte
			if seed.studioKind == "audio" {
				metadata = []byte(`{"duration_seconds": 3}`)
			}
			artifactID, err := r.saveArtifact(ctx, q, userID, title, seed, metadata, stored)
			if err != nil {
				return err
			}
			answer.ArtifactID = &artifactID
		}
		output := SeedOutputParams{
			NotebookID: asked.NotebookID, UserID: userID, Kind: seed.studioKind, Title: title,
			Options: []byte("{}"), SourceDocumentIds: sources,
		}
		if options, has := seedOptions[seed.studioKind]; has {
			output.Options = []byte(options)
		}
		if seed.studioKind == "report" {
			output.Options = []byte(`{"format": "document"}`)
			if asked.Kind == "report_html" {
				output.Options = []byte(`{"format": "interactive"}`)
			}
		}
		if ready {
			output.GeneratedBy = []byte(`{"model": "e2e-fake-model"}`)
		}
		if seed.studioKind == "audio" {
			podcast := SeedPodcastParams{UserID: userID, Title: title, ArtifactID: answer.ArtifactID}
			switch {
			case ready:
				duration := int64(3)
				podcast.Status, podcast.DurationSeconds = "ready", &duration
			case running:
				podcast.Status = "rendering"
			case failed:
				reason := "E2E seeded failure"
				podcast.Status, podcast.Error = "failed", &reason
			}
			id, err := q.SeedPodcast(ctx, podcast)
			if err != nil {
				return err
			}
			output.PodcastID = &id
		} else {
			name, err := newID()
			if err != nil {
				return err
			}
			job := SeedJobParams{
				UserID: userID, Kind: "artifact", Title: title,
				ToolCallID: "notebook-studio:" + strconv.FormatInt(asked.NotebookID, 10) + ":e2e-" + name,
				Request:    mustJSON(map[string]string{"kind": seed.studioKind}),
				Status:     DeliverableJobStatus(asked.Status), ArtifactID: answer.ArtifactID,
			}
			if seed.studioKind == "video" {
				job.Kind = "video"
			}
			switch {
			case ready:
				phase := "done"
				job.Phase, job.Progress, job.FinishedAt = &phase, 100, &now
			case running:
				phase, beat := "rendering", now.Add(24*time.Hour)
				job.Phase, job.Progress, job.HeartbeatAt = &phase, 40, &beat
			case failed:
				phase, code := "failed", "generation_failed"
				job.Phase, job.FailureCode, job.FinishedAt = &phase, &code, &now
			}
			id, err := q.SeedJob(ctx, job)
			if err != nil {
				return err
			}
			output.DeliverableJobID = &id
		}
		answer.OutputID, err = q.SeedOutput(ctx, output)
		return err
	})
	if x.failed("could not seed the studio output", err) {
		return
	}
	x.send(http.StatusOK, answer)
}

// completeOutput finishes an output the app itself queued (from the
// studio dialog, or a report suggestion's Add) with that kind's seeded
// files. An end-to-end stack has no sandbox and no model to finish it, so
// this stands in for the run: the job is marked ready with its artifact,
// as a finished run leaves it.
func (r Routes) completeOutput(x *exchange) {
	var asked struct {
		NotebookID int64 `json:"notebook_id"`
		OutputID   int64 `json:"output_id"`
	}
	if !x.body(&asked) {
		return
	}
	ctx := x.ctx.Context()
	answer := outputAnswer{OutputID: asked.OutputID}
	err := r.inTransaction(ctx, func(q *Queries, stored *[]string) error {
		output, err := q.QueuedOutput(ctx, QueuedOutputParams{OutputID: asked.OutputID, NotebookID: asked.NotebookID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errAbsent("no queued output")
		}
		if err != nil {
			return err
		}
		seed, err := filesFor(output.Kind, output.Title)
		if err != nil {
			return err
		}
		artifactID, err := r.saveArtifact(ctx, q, output.UserID, output.Title, seed, nil, stored)
		if err != nil {
			return err
		}
		answer.ArtifactID = &artifactID
		return q.FinishJob(ctx, FinishJobParams{ArtifactID: &artifactID, Now: r.now(), ID: *output.DeliverableJobID, UserID: output.UserID})
	})
	if x.failed("could not complete the output", err) {
		return
	}
	x.send(http.StatusOK, answer)
}
