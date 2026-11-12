package videojob

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_studio/artifactsave"
)

// deliver keeps the verified video: the MP4 streamed out of the sandbox
// into the file store, each scene's narration beside it, then the artifact's
// rows and the job's "ready" in one transaction.
//
// The files are stored before the transaction, under the artifact's
// reserved id, so the transaction does not stay open for the length of a
// hundred-megabyte transfer; whatever the transaction does not commit, they
// are removed. The Python worker saved and committed the artifact and then,
// in a second step, marked the job ready, so a cancel between the two left
// a video in the account whose card said cancelled.
func (j *Job) deliver(ctx context.Context, log *slog.Logger, job Claimed, box Sandbox, output string, video Authored, voices []voiced, checked verified) error {
	artifactID, err := j.Rows.ReserveArtifactID(ctx)
	if err != nil {
		return fail(codeGeneration, fmt.Errorf("could not reserve the artifact's id: %w", err))
	}
	prefix := "artifacts/" + job.UserID + "/" + strconv.FormatInt(artifactID, 10)
	var stored []string
	keep := false
	defer func() {
		if keep {
			return
		}
		// A blob no row points at would never be found again.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		for _, key := range stored {
			if err := j.Files.Remove(cleanup, key); err != nil {
				log.Warn("a stored file of a video that was not kept could not be removed", "key", key, "error", err.Error())
			}
		}
	}()

	stream, err := box.OpenFile(ctx, output)
	if err != nil {
		return fail(codeGeneration, fmt.Errorf("could not read the video out of the sandbox: %w", err))
	}
	key := prefix + "/primary/" + randomName() + ".mp4"
	put, err := j.Files.PutStream(ctx, key, stream, "video/mp4")
	stream.Close()
	if err != nil {
		return fail(codeGeneration, fmt.Errorf("could not store the video: %w", err))
	}
	stored = append(stored, key)
	// The bytes stored are the bytes verified, or nothing is kept. This is
	// what the Python tier's signed receipt in the sandbox was for.
	if put.SHA256 != checked.SHA256 {
		return fail(codeVerification, fmt.Errorf("the stored video (sha256 %s) is not the one that was verified (%s)", put.SHA256, checked.SHA256))
	}

	audio := make([]slideAudio, 0, len(voices))
	for _, voice := range voices {
		slideKey := prefix + "/slides/slide-" + strconv.Itoa(voice.Number) + "-" + randomName() + ".mp3"
		if err := j.Files.Put(ctx, slideKey, voice.MP3, "audio/mpeg"); err != nil {
			return fail(codeGeneration, fmt.Errorf("could not store scene %d's narration: %w", voice.Number, err))
		}
		stored = append(stored, slideKey)
		audio = append(audio, slideAudio{Number: voice.Number, Key: slideKey})
	}
	meta, err := metadata(video, voices, audio, j.Files.Name(), checked.Seconds)
	if err != nil {
		return fail(codeGeneration, err)
	}
	created, delivered, err := j.Rows.Deliver(ctx, job, artifactsave.Rows{
		ID: artifactID, UserID: job.UserID, ToolCallID: job.ToolCallID, Title: job.Title, Format: "video",
		Markdown: markdown(job.Title, video), Metadata: meta,
		Primary: artifactsave.StoredFile{Backend: j.Files.Name(), Key: key, Filename: videoFilename(job.Title, job.ID),
			MimeType: "video/mp4", Size: put.Size, SHA256: put.SHA256},
	})
	if err != nil {
		return fail(codeGeneration, fmt.Errorf("could not save the video: %w", err))
	}
	if !delivered {
		return j.why(ctx, job)
	}
	keep = true
	// The video is kept and plays whether or not its text is searchable
	// yet, so a failure to queue the indexing does not fail it.
	if err := j.Indexer.Index(context.WithoutCancel(ctx), created.DocumentID); err != nil {
		log.Error("the video is kept and its document could not be queued for indexing", "document", created.DocumentID, "error", err.Error())
	}
	return nil
}

// randomName is a fresh name for a stored object: an object is written once
// and never replaced.
func randomName() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}
