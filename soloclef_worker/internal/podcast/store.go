package podcast

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// The statuses this job reads and writes (`podcast_status`).
const (
	statusDrafting  = "drafting"
	statusRendering = "rendering"
	statusReady     = "ready"
)

// ErrGone is a podcast that no longer exists.
var ErrGone = errors.New("podcast: the podcast is gone")

// Run is one `podcast_runs` row as the job reads it.
type Run struct {
	ID         int64
	UserID     string
	Status     string
	Title      string
	Source     string
	Spec       []byte
	Transcript []byte
	ArtifactID *int64
	ThreadID   *int64
}

// Delivery is a finished episode, ready to be kept.
type Delivery struct {
	Run      Run
	Markdown string
	Audio    []byte
	Seconds  float64
}

// Rows is the job's database. Every statement after Load names the owner
// beside the podcast, so a row can only ever be written for the account
// that owns it.
type Rows interface {
	// Load reads the podcast by id; the message names nothing else, and the
	// owner is read off the row, which is the one source that cannot be
	// wrong about it.
	Load(ctx context.Context, podcastID int64) (Run, error)
	// Status is the podcast's status now, or ErrGone.
	Status(ctx context.Context, podcastID int64, userID string) (string, error)
	// AttachTranscript stores the transcript and moves the podcast from
	// drafting to rendering. It reports false, and writes nothing, when the
	// podcast is no longer drafting.
	AttachTranscript(ctx context.Context, podcastID int64, userID string, transcript []byte) (bool, error)
	// Deliver keeps the episode as the podcast's artifact and moves it from
	// rendering to ready. It reports false, and keeps nothing, when the
	// podcast is no longer rendering. The document it returns is the
	// artifact's, to be indexed.
	Deliver(ctx context.Context, delivery Delivery) (documentID int64, delivered bool, err error)
	// Fail records why the podcast failed, if it is still drafting or
	// rendering. A podcast cancelled meanwhile stays cancelled.
	Fail(ctx context.Context, podcastID int64, userID string, reason string) error
}

// Store is Rows over Postgres, with the file store the audio is kept in.
type Store struct {
	Pool  *pgxpool.Pool
	Files filestore.Writer
}

func (s Store) Load(ctx context.Context, podcastID int64) (Run, error) {
	var run Run
	var source *string
	err := s.Pool.QueryRow(ctx, `
		SELECT id, user_id::text, status::text, title, source_content, spec, podcast_transcript, artifact_id, thread_id
		FROM podcast_runs WHERE id = $1`, podcastID).Scan(
		&run.ID, &run.UserID, &run.Status, &run.Title, &source, &run.Spec, &run.Transcript, &run.ArtifactID, &run.ThreadID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrGone
	}
	if err != nil {
		return Run{}, fmt.Errorf("reading podcast %d: %w", podcastID, err)
	}
	if source != nil {
		run.Source = *source
	}
	return run, nil
}

func (s Store) Status(ctx context.Context, podcastID int64, userID string) (string, error) {
	var status string
	err := s.Pool.QueryRow(ctx, `SELECT status::text FROM podcast_runs WHERE id = $1 AND user_id = $2::uuid`,
		podcastID, userID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrGone
	}
	return status, err
}

// lockStatus reads the status under a row lock held until the transaction
// ends. That is the cancel fix the Python tasks carry (tasks/draft.py,
// render.py): the draft and the render take minutes, and a cancel made in
// that time must not be written over. With the row locked from this read
// to the commit, a cancel either lands first, and is seen here, or waits
// and finds the podcast already moved on, which its own guarded UPDATE
// then refuses.
func lockStatus(ctx context.Context, tx pgx.Tx, podcastID int64, userID string) (string, *int64, error) {
	var (
		status   string
		artifact *int64
	)
	err := tx.QueryRow(ctx, `
		SELECT status::text, artifact_id FROM podcast_runs
		WHERE id = $1 AND user_id = $2::uuid FOR UPDATE`, podcastID, userID).Scan(&status, &artifact)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil
	}
	return status, artifact, err
}

func (s Store) AttachTranscript(ctx context.Context, podcastID int64, userID string, transcript []byte) (bool, error) {
	attached := false
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		status, _, err := lockStatus(ctx, tx, podcastID, userID)
		if err != nil || status != statusDrafting {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE podcast_runs SET podcast_transcript = $3, status = 'rendering', error = NULL
			WHERE id = $1 AND user_id = $2::uuid`, podcastID, userID, transcript)
		attached = err == nil
		return err
	})
	return attached, err
}

func (s Store) Fail(ctx context.Context, podcastID int64, userID string, reason string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE podcast_runs SET status = 'failed', error = $3
		WHERE id = $1 AND user_id = $2::uuid AND status IN ('drafting', 'rendering')`, podcastID, userID, reason)
	return err
}

// removeTimeout bounds the removal of a file whose rows did not commit, or
// of the episode a new take replaced.
const removeTimeout = 15 * time.Second

func (s Store) Deliver(ctx context.Context, delivery Delivery) (int64, bool, error) {
	run := delivery.Run
	var (
		documentID int64
		delivered  bool
		storedKey  string
		replaced   *storedFile
	)
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		status, artifactID, err := lockStatus(ctx, tx, run.ID, run.UserID)
		if err != nil || status != statusRendering {
			return err
		}
		metadata, err := json.Marshal(map[string]any{
			"legacy":           map[string]any{"kind": "podcast", "id": run.ID},
			"duration_seconds": int(math.Round(delivery.Seconds)),
		})
		if err != nil {
			return err
		}
		episode := episode{tx: tx, run: run, markdown: delivery.Markdown, metadata: metadata}

		// A new take of a finished episode is a new generation of the same
		// artifact, so the link to it and its place in the Studio stay put.
		// An artifact deleted meanwhile is made again.
		var id int64
		if artifactID != nil {
			id, documentID, replaced, err = episode.renew(ctx, *artifactID)
			if err != nil {
				return err
			}
		}
		if id == 0 {
			if id, documentID, err = episode.create(ctx); err != nil {
				return err
			}
		}

		storedKey = "artifacts/" + run.UserID + "/" + strconv.FormatInt(id, 10) + "/primary/" + randomName() + ".mp3"
		if err := s.Files.Put(ctx, storedKey, delivery.Audio, "audio/mpeg"); err != nil {
			storedKey = ""
			return fmt.Errorf("storing the episode: %w", err)
		}
		sum := sha256.Sum256(delivery.Audio)
		if _, err := tx.Exec(ctx, `
			INSERT INTO artifact_files (artifact_id, role, storage_backend, storage_key, original_filename,
			                            mime_type, size_bytes, checksum_sha256)
			VALUES ($1, 'primary', $2, $3, $4, 'audio/mpeg', $5, $6)`,
			id, s.Files.Name(), storedKey, slug(run.Title, "podcast")+".mp3", len(delivery.Audio), hex.EncodeToString(sum[:])); err != nil {
			return err
		}
		seconds := int(math.Round(delivery.Seconds))
		if _, err := tx.Exec(ctx, `
			UPDATE podcast_runs SET status = 'ready', artifact_id = $3, duration_seconds = $4, error = NULL
			WHERE id = $1 AND user_id = $2::uuid`, run.ID, run.UserID, id, seconds); err != nil {
			return err
		}
		delivered = true
		return nil
	})
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), removeTimeout)
	defer cancel()
	if err != nil {
		// A blob no row points at would never be found again.
		if storedKey != "" {
			_ = s.Files.Remove(cleanup, storedKey)
		}
		return 0, false, err
	}
	if replaced != nil {
		// After the commit: until then the old take was still the one
		// served. A failure leaves an object no row names, which is logged
		// by the caller's error and harms nothing.
		if replaced.backend != s.Files.Name() {
			return documentID, delivered, fmt.Errorf("the new take is kept and the old one's file %q is in the %s store, which this worker does not write to", replaced.key, replaced.backend)
		}
		if err := s.Files.Remove(cleanup, replaced.key); err != nil {
			return documentID, delivered, fmt.Errorf("the new take is kept and the old one's file %q could not be removed: %w", replaced.key, err)
		}
	}
	return documentID, delivered, nil
}

type storedFile struct{ backend, key string }

// episode writes an episode's artifact rows inside the delivery's
// transaction. The rows are the ones the API tier writes for an artifact
// (soloclef_assistant/deliverables/store.go), so its list, viewer and delete
// treat a podcast like any other artifact.
type episode struct {
	tx       pgx.Tx
	run      Run
	markdown string
	metadata []byte
}

// pathAttempts bounds the search for a free document name.
const pathAttempts = 50

func (e episode) create(ctx context.Context) (artifactID, documentID int64, err error) {
	path := ""
	for attempt := 1; attempt <= pathAttempts; attempt++ {
		candidate := "/documents/" + documentName(e.run.Title, attempt)
		var taken bool
		if err := e.tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM documents WHERE user_id = $1::uuid AND path = $2)`,
			e.run.UserID, candidate).Scan(&taken); err != nil {
			return 0, 0, err
		}
		if !taken {
			path = candidate
			break
		}
	}
	if path == "" {
		return 0, 0, fmt.Errorf("no free document name for %q after %d attempts", e.run.Title, pathAttempts)
	}
	content, identifier := documentHashes(e.run.UserID, path, e.markdown)
	// status "pending": the document is not searchable until the indexer
	// has chunked it, and the list says so.
	if err := e.tx.QueryRow(ctx, `
		INSERT INTO documents (title, document_type, document_metadata, content, content_hash,
		                       unique_identifier_hash, path, source_markdown, updated_at, user_id, status)
		VALUES ($1, 'ARTIFACT', '{}'::jsonb, $2, $3, $4, $5, $2, now(), $6::uuid, '{"state": "pending"}'::jsonb)
		RETURNING id`, e.run.Title, e.markdown, content, identifier, path, e.run.UserID).Scan(&documentID); err != nil {
		return 0, 0, err
	}
	if err := e.tx.QueryRow(ctx, `
		INSERT INTO artifacts (document_id, user_id, thread_id, format, generation, metadata)
		VALUES ($1, $2::uuid, $3, 'podcast', 1, $4)
		RETURNING id`, documentID, e.run.UserID, e.run.ThreadID, e.metadata).Scan(&artifactID); err != nil {
		return 0, 0, err
	}
	_, err = e.tx.Exec(ctx, `
		UPDATE documents SET document_metadata = jsonb_build_object('artifact_id', $1::bigint)
		WHERE id = $2 AND user_id = $3::uuid`, artifactID, documentID, e.run.UserID)
	return artifactID, documentID, err
}

// renew makes the existing artifact the new take: one generation more, its
// document's text replaced, and its file row dropped for the one the caller
// writes next. It returns 0 when the artifact is gone.
func (e episode) renew(ctx context.Context, artifactID int64) (int64, int64, *storedFile, error) {
	var documentID int64
	err := e.tx.QueryRow(ctx, `
		UPDATE artifacts SET generation = generation + 1, metadata = $3, updated_at = now()
		WHERE id = $1 AND user_id = $2::uuid
		RETURNING document_id`, artifactID, e.run.UserID, e.metadata).Scan(&documentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, nil, nil
	}
	if err != nil {
		return 0, 0, nil, err
	}
	var path string
	if err := e.tx.QueryRow(ctx, `SELECT COALESCE(path, '') FROM documents WHERE id = $1 AND user_id = $2::uuid`,
		documentID, e.run.UserID).Scan(&path); err != nil {
		return 0, 0, nil, err
	}
	content, _ := documentHashes(e.run.UserID, path, e.markdown)
	if _, err := e.tx.Exec(ctx, `
		UPDATE documents SET title = $3, content = $4, source_markdown = $4, content_hash = $5,
		       status = '{"state": "pending"}'::jsonb, updated_at = now()
		WHERE id = $1 AND user_id = $2::uuid`, documentID, e.run.UserID, e.run.Title, e.markdown, content); err != nil {
		return 0, 0, nil, err
	}
	var old storedFile
	err = e.tx.QueryRow(ctx, `
		DELETE FROM artifact_files WHERE artifact_id = $1 AND role = 'primary'
		RETURNING storage_backend, storage_key`, artifactID).Scan(&old.backend, &old.key)
	if errors.Is(err, pgx.ErrNoRows) {
		return artifactID, documentID, nil, nil
	}
	if err != nil {
		return 0, 0, nil, err
	}
	return artifactID, documentID, &old, nil
}

// randomName is a fresh name for a stored object: an object is written once
// and never replaced.
func randomName() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

// slug, documentName and documentHashes are the API tier's own
// (soloclef_assistant/deliverables/save.go), so a podcast's file and document
// are named, deduplicated and found the way every other artifact's are.

func slug(title, format string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
		if b.Len() >= 80 {
			break
		}
	}
	if b.Len() == 0 {
		return format
	}
	return b.String()
}

func documentName(title string, attempt int) string {
	var b strings.Builder
	for _, r := range title {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			r = ' '
		}
		b.WriteRune(r)
	}
	name := strings.Trim(strings.Join(strings.Fields(b.String()), " "), ". ")
	if runes := []rune(name); len(runes) > 120 {
		name = strings.TrimSpace(string(runes[:120]))
	}
	if name == "" {
		name = "untitled"
	}
	if attempt > 1 {
		name += " (" + strconv.Itoa(attempt) + ")"
	}
	return name + ".md"
}

func documentHashes(userID, path, markdown string) (content, identifier string) {
	return sha256Hex(userID + ":" + markdown), sha256Hex(fmt.Sprintf("NOTE:%s:%s", path, userID))
}

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
