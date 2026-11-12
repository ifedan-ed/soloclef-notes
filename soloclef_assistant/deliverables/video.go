package deliverables

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// A video (slides with narration) is not made in the turn. The specialist
// queues a render job: a row in deliverable_jobs, and a message on the heavy
// queue telling a worker to run it. The worker writes the scenes, voices the
// narration, renders and saves the video, which takes many minutes; the
// page's video card follows the row live from the moment it exists. So this
// file is the whole of what chat does for a video: check, write the row,
// publish.
//
// # The worker's contract
//
// The render is the Python worker's (app/tasks/celery_tasks/
// deliverable_job_tasks.py) and has not been ported yet: until it is, the
// Go worker answers the message by marking the job failed at once
// (soloclef_worker/internal/videojob), so the card does not spin. The
// contract below is the one the port keeps. The worker is told by:
//
//   - task "deliverables.execute_queued", keyword argument job_id, no
//     positional arguments;
//   - on the heavy queue ("<default queue>.heavy"; the routing table is
//     soloclef_celery/routing, the same list Celery's task_routes has);
//   - published under the task id the row's celery_task_id names,
//     "deliverable-job:<id>:attempt:1". The worker claims a job only for
//     the message whose id matches, so that a duplicate delivery or an old
//     attempt can never run a job twice.
//
// It reads the job's request column as its VideoJobRequestV1
// (app/deliverables/video/executor.py), which refuses unknown fields:
// version, brief, source_references, revision_artifact_id, root_thread_id,
// source_material, source_condensed, max_seconds. videoRequest below is
// that shape.
//
// The row is committed before the message is published, and is its own
// outbox: if publishing fails, the minute-by-minute reconciler
// ("deliverables.reconcile_queued") publishes every queued row that has
// gone stale, under the same id. So a failed publish is logged and the job
// is still reported as started, because it is.
//
// # What this does better than the tool it replaces
//
//   - The video is written from the material. The old tool queued a brief
//     and a list of source names; the worker then wrote the video from the
//     brief alone, filling in from the model's general knowledge. The
//     material the specialist was handed is now the job's source_material,
//     which the worker already knows how to fit to its model's context.
//   - A second run of the same call returns the job the first one queued
//     only if it is the same thread's; a provider that reuses a call id
//     cannot hand one conversation another's video.

// VideoTool is keyed to the page's video card (components/tool-ui/
// deliverable-job).
const VideoTool = "enqueue_deliverable_job"

// kindVideo is the kind whose finisher is VideoTool, and the value of the
// row's kind column.
const kindVideo = "video"

// VideoTask is the task a queued video is run by.
const VideoTask = "deliverables.execute_queued"

// The bounds of a video request. They are the worker's own
// (VideoJobRequestV1): a row written past them is a job that fails the
// moment it is claimed.
const (
	maxVideoTitleChars = 500
	maxVideoBriefChars = 16_000
	// maxVideoSourceChars is the worker's VIDEO_SOURCE_MAX_CHARS: 60,000
	// tokens at its three characters a token.
	maxVideoSourceChars = 180_000
)

const videoInstructions = `Video: narrated slides, rendered as a video file.

Call ` + VideoTool + ` once, with:
- title: the video's title, one line, up to 500 characters.
- brief: what the video should be, in plain sentences: the subject, the audience, the angle, and how long if the task says. Up to 16000 characters.
- source_content: ALL the material the video should be made from, copied in full from the task. It is the only thing the video is written from; do not summarise it and do not pass a topic alone.

Do not call save_artifact for a video. The tool does not make the video: it queues it, and it is written, narrated and rendered over the next several minutes while a card in the chat shows its progress. So answer only that the video is being generated and that the user can keep chatting. Do not say how long it will take, and do not describe its status.`

// VideoRequest is a video to queue: what the specialist's model supplied.
type VideoRequest struct {
	Title string
	// Brief says what the video should be.
	Brief string
	// Material is the text the video is written from, or "" when the task
	// gave only a subject.
	Material string
}

// videoRequest is the job's request column, the worker's VideoJobRequestV1.
// Fields the worker treats as optional are left out when unset; it refuses
// a null where it wants a string.
type videoRequest struct {
	Version int    `json:"version"`
	Brief   string `json:"brief"`
	// SourceReferences are names of account sources. Chat passes the
	// text itself (SourceMaterial), so the list is always empty; it is
	// written because the field is part of the shape.
	SourceReferences []string `json:"source_references"`
	RootThreadID     int64    `json:"root_thread_id"`
	SourceMaterial   string   `json:"source_material,omitempty"`
}

// newVideoRequest checks what the model supplied and returns it as it will
// be stored.
func newVideoRequest(title, brief, material string) (VideoRequest, error) {
	var problems []string
	title = strings.Join(strings.Fields(title), " ")
	switch {
	case title == "":
		problems = append(problems, "title is empty")
	case utf8.RuneCountInString(title) > maxVideoTitleChars:
		problems = append(problems, fmt.Sprintf("title is longer than %d characters", maxVideoTitleChars))
	}
	// On one line, as the worker stores it: it collapses the brief's white
	// space itself and then holds the result to the limit.
	brief = strings.Join(strings.Fields(brief), " ")
	switch {
	case brief == "":
		problems = append(problems, "brief is empty")
	case utf8.RuneCountInString(brief) > maxVideoBriefChars:
		problems = append(problems, fmt.Sprintf("brief is longer than %d characters", maxVideoBriefChars))
	}
	// The material keeps its line breaks: they mark the sections the
	// worker keeps when it has to shorten a long source.
	material = strings.TrimSpace(material)
	if utf8.RuneCountInString(material) > maxVideoSourceChars {
		problems = append(problems, fmt.Sprintf("source_content is longer than %d characters", maxVideoSourceChars))
	}
	for name, text := range map[string]string{"title": title, "brief": brief, "source_content": material} {
		if strings.ContainsRune(text, 0) {
			problems = append(problems, name+" contains a NUL character")
		}
	}
	if len(problems) > 0 {
		return VideoRequest{}, refusal("the video was not queued", problems)
	}
	return VideoRequest{Title: title, Brief: brief, Material: material}, nil
}

// VideoQueuer queues a video for the turn's owner and answers the job's id.
// It is an interface so that the specialist is tested without a database or
// a broker.
//
// An error is a failure whose text stays in the log; the person is told a
// plain sentence.
type VideoQueuer interface {
	// Check reports what is missing. Called once at start-up.
	Check() error
	Queue(ctx context.Context, owner Owner, toolCallID string, request VideoRequest) (jobID int64, err error)
}

// QueuedVideo is a job as CreateVideo found or made it.
type QueuedVideo struct {
	JobID int64
	// TaskID is the id the job's message must be published under.
	TaskID string
	// Created is false when an earlier run of the same call had already
	// queued the job.
	Created bool
}

// VideoRows writes the job.
type VideoRows interface {
	// CreateVideo writes the job for the owner's own thread, or finds the
	// one an earlier run of the same call wrote.
	CreateVideo(ctx context.Context, owner Owner, toolCallID, title string, request []byte) (QueuedVideo, error)
}

// Videos is VideoQueuer over the job table and the task queue.
type Videos struct {
	Rows VideoRows
	// Publish puts the job's message on the queue, under taskID.
	Publish func(ctx context.Context, jobID int64, taskID string) error
	Logger  *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (v Videos) Check() error {
	switch {
	case v.Rows == nil:
		return errors.New("deliverables: Videos.Rows is not set")
	case v.Publish == nil:
		return errors.New("deliverables: Videos.Publish is not set")
	case v.Logger == nil:
		return errors.New("deliverables: Videos.Logger is not set")
	}
	return nil
}

// VideoMessage is the message that asks the worker to render a queued
// video, as the contract above describes it. A tier publishes it on its own
// queue under taskID, the one id the job's row lets claim it; this is here
// so that every tier that queues a video sends the same bytes.
func VideoMessage(jobID int64, taskID string) produce.Message {
	return produce.Message{
		Task: VideoTask, Kwargs: map[string]any{"job_id": jobID}, ID: taskID,
		// send_task with keyword arguments only writes its (empty)
		// positional arguments as a tuple, like .delay().
		ArgsAsTuple: true,
	}
}

// Queue writes the job and publishes it.
func (v Videos) Queue(ctx context.Context, owner Owner, toolCallID string, request VideoRequest) (int64, error) {
	if toolCallID == "" {
		// The call's id is the job's idempotency key; without one, every
		// retry would be another video.
		return 0, errors.New("deliverables: a video cannot be queued without the id of the call that asked for it")
	}
	column, err := json.Marshal(videoRequest{
		Version: 1, Brief: request.Brief, SourceReferences: []string{},
		RootThreadID: owner.ThreadID, SourceMaterial: request.Material,
	})
	if err != nil {
		return 0, err
	}
	queued, err := v.Rows.CreateVideo(ctx, owner, toolCallID, request.Title, column)
	if err != nil {
		return 0, err
	}
	if !queued.Created {
		// An earlier run of this call queued it and either published it or
		// left it for the reconciler. Publishing again would be harmless
		// (the worker ignores a second message for a claimed job) and is
		// not needed.
		return queued.JobID, nil
	}
	// After the commit, never inside it: a worker that received the message
	// before the row was visible would find nothing to claim and drop it.
	if err := v.Publish(ctx, queued.JobID, queued.TaskID); err != nil {
		// The row is queued and the reconciler publishes stale queued rows
		// every minute, so the video is late, not lost.
		v.Logger.LogAttrs(ctx, slog.LevelError, "a queued video could not be published; the reconciler will publish it",
			slog.Int64("job_id", queued.JobID), slog.String("error", err.Error()))
	}
	return queued.JobID, nil
}

// VideoStore is VideoRows over the pool.
type VideoStore struct {
	Pool *pgxpool.Pool
}

// errForeignVideo is a call id that already belongs to a job of another
// thread.
var errForeignVideo = errors.New("deliverables: the call's id already names a video of another conversation")

func (s VideoStore) CreateVideo(ctx context.Context, owner Owner, toolCallID, title string, request []byte) (QueuedVideo, error) {
	var queued QueuedVideo
	// One transaction: the insert that finds the call already has a job and
	// the read of that job see the same rows.
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var err error
		queued, err = s.createVideo(ctx, New(tx), owner, toolCallID, title, request)
		return err
	})
	if err != nil {
		return QueuedVideo{}, err
	}
	return queued, nil
}

func (s VideoStore) createVideo(ctx context.Context, queries *Queries, owner Owner, toolCallID, title string, request []byte) (QueuedVideo, error) {
	{
		threadID := owner.ThreadID
		created, err := queries.CreateVideoJob(ctx, CreateVideoJobParams{
			Title: title, UserID: owner.UserID, ThreadID: threadID,
			ToolCallID: toolCallID, Request: request,
		})
		if err == nil {
			if created.CeleryTaskID == nil {
				return QueuedVideo{}, errors.New("deliverables: the queued video has no task id")
			}
			return QueuedVideo{JobID: created.ID, TaskID: *created.CeleryTaskID, Created: true}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return QueuedVideo{}, err
		}
		// The call has a job already: this is a second run of it.
		existing, err := queries.VideoJobForToolCall(ctx, VideoJobForToolCallParams{UserID: owner.UserID, ToolCallID: toolCallID})
		if err != nil {
			return QueuedVideo{}, err
		}
		if existing.ThreadID == nil || *existing.ThreadID != owner.ThreadID {
			return QueuedVideo{}, errForeignVideo
		}
		taskID := ""
		if existing.CeleryTaskID != nil {
			taskID = *existing.CeleryTaskID
		}
		return QueuedVideo{JobID: existing.ID, TaskID: taskID}, nil
	}
}

// VideoQueued is what enqueue_deliverable_job answers: what the model is
// told, and, less its message, what the page's video card draws from.
type VideoQueued struct {
	// Status is "pending": the card then follows the job by its id.
	Status  string `json:"status"`
	JobID   int64  `json:"job_id"`
	Title   string `json:"title"`
	Message string `json:"message,omitempty"`
}

// VideoCard is what the page's video card draws from. With a job id it
// follows the job live; with the status "failed" it says the video could
// not be started.
type VideoCard struct {
	Status string `json:"status"`
	JobID  int64  `json:"job_id,omitempty"`
	Title  string `json:"title,omitempty"`
	Error  string `json:"error,omitempty"`
}

const (
	statusPending      = "pending"
	videoQueuedMessage = "The video is queued and is being generated. A card in the chat shows its progress and the finished video; the user can keep chatting. Do not state its status or how long it will take."
	// refusedVideoNote follows a refusal's sentence in what the model
	// reads, and is taken off again before the sentence reaches the card.
	refusedVideoNote = " The video was not queued; tell the requester exactly that."
)

func (s Specialist) videoTool(owner Owner) tool.Tool {
	return tool.Tool{
		Name:        VideoTool,
		Description: "Queue a narrated video made from the material. It is written, narrated and rendered over the next several minutes while a card in the chat shows its progress. It answers the job that was queued, or every problem to fix.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","description":"The video's title, one line, up to 500 characters."},"brief":{"type":"string","description":"What the video should be: subject, audience, angle, length if asked. Up to 16000 characters."},"source_content":{"type":"string","description":"All the material the video is made from, copied in full."}},"required":["title","brief"],"additionalProperties":false}`),
		// The user asked this chat for a video; the job is theirs, is
		// shown on a card with a cancel button from the moment it exists,
		// and sends nothing outside the account. It does not ask first.
		Approval: tool.Auto,
		Run: func(ctx context.Context, call tool.Call) (string, error) {
			var args struct {
				Title    string `json:"title"`
				Brief    string `json:"brief"`
				Material string `json:"source_content"`
			}
			decoder := json.NewDecoder(strings.NewReader(string(call.Args)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&args); err != nil {
				return "", fmt.Errorf("the arguments do not fit the schema: %w", err)
			}
			request, err := newVideoRequest(args.Title, args.Brief, args.Material)
			if err != nil {
				return "", err
			}
			jobID, err := s.Videos.Queue(ctx, owner, call.ID, request)
			if err != nil {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				s.Saver.Logger.ErrorContext(ctx, "a video could not be queued from chat", "error", err.Error(), "user_id", owner.UserID)
				return "", errors.New("the video could not be queued because of a storage error." + refusedVideoNote)
			}
			out, err := json.Marshal(VideoQueued{Status: statusPending, JobID: jobID, Title: request.Title, Message: videoQueuedMessage})
			return string(out), err
		},
	}
}

// videoCard is the page's video card for a finished enqueue call.
func videoCard(result event.Event) (any, bool) {
	if result.IsError {
		// Without this the page would show the default card's "completed"
		// for a video that never started.
		return VideoCard{Status: statusFailed, Error: cardSentence(result.Text, refusedVideoNote)}, true
	}
	var queued VideoQueued
	if json.Unmarshal([]byte(result.Text), &queued) != nil || queued.JobID <= 0 || queued.Status == "" {
		return nil, false
	}
	return VideoCard{Status: queued.Status, JobID: queued.JobID, Title: queued.Title}, true
}

// videoTaskID is the id a job's first attempt is published under. It is
// written by the statement that creates the row; this is the same spelling
// for tests and logs.
func videoTaskID(jobID int64) string {
	return "deliverable-job:" + strconv.FormatInt(jobID, 10) + ":attempt:1"
}
