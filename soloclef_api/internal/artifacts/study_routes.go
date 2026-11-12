package artifacts

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// This file is the six study-state writes: three for a flashcard deck, three
// for a quiz. Each answers with the caller's whole state after the write,
// which the web app puts straight into its cached manifest.
//
// # The shape every one of them has
//
//  1. Parameters and body, all judged at once (422), then the gate (403).
//  2. Read the artifact: it must exist, be the right format (404), and be at
//     the generation the client believes it is (409 -- the deck was revised
//     while the person had it open, and the web app reloads it).
//  3. Read the study file, for the count and the answer key: out of the file
//     store the first time, from memory after that (StudyFiles). No lock is
//     held yet.
//  4. Lock the row; check the format and generation *again*, because step 3
//     took time and a revision may have landed; apply the change; store it.
//
// Steps 1 to 3 and the frame of 4 are `studyWrite` below. What differs
// between the six is a body and one small function over a state, and those
// are the pure functions in flashcard_state.go and quiz_state.go.

// StudyRows is what a study-state write asks of the database.
type StudyRows interface {
	StudySourceOf(ctx context.Context, userID string, artifactID int64) (*StudySource, error)
	ChangeStudy(ctx context.Context, userID string, artifactID int64, section, holder string,
		decide func(locked *LockedStudy) StudyWrite) error
}

// studyKind is what differs between the two formats in the frame of a
// write: where the state is kept and what the refusals say.
type studyKind struct {
	format, section, holder string
	absent                  string
	revised                 string
	unreadable              string
}

var (
	flashcardKind = studyKind{
		format: flashcardsFormat, section: flashcardSection, holder: flashcardHolder,
		absent:     "Flashcard artifact not found",
		revised:    "This flashcard deck was revised. Refresh before updating it.",
		unreadable: "This flashcard deck cannot be studied: ",
	}
	quizKind = studyKind{
		format: quizFormat, section: quizSection, holder: quizHolder,
		absent:     "Quiz artifact not found",
		revised:    "This quiz was revised. Refresh before updating it.",
		unreadable: "This quiz cannot be studied: ",
	}
)

// studyWrite is steps 2 to 4 of every study-state write.
//
// `load` reads the study file and keeps what the route needs from it.
// `apply` is called under the row lock with the section's stored states and
// returns what to store (nil to remove the section), the caller's state to
// answer with, and a refusal if the change is not allowed.
func (g Gate) studyWrite(
	ctx huma.Context,
	response http.ResponseWriter,
	request admitted,
	rows StudyRows,
	kind studyKind,
	generation int64,
	load func(file *StoredFile) error,
	apply func(states []byte) (next []byte, answer *pythonjson.Object, refusal *Refusal, err error),
) {
	source, err := rows.StudySourceOf(ctx.Context(), request.caller.UserID, request.artifactID)
	if err != nil {
		g.fail(ctx.Context(), response, request.requestID, "could not read the artifact", err)
		return
	}
	// An artifact of another format is "not found" here, not "wrong type":
	// there is no flashcard progress on a spreadsheet to be found.
	if source == nil || source.Format != kind.format {
		g.Errors.Write(response, http.StatusNotFound, kind.absent, request.requestID)
		return
	}
	if source.Generation != generation {
		g.Errors.Write(response, http.StatusConflict, kind.revised, request.requestID)
		return
	}

	var notAStudyFile unreadable
	if err := load(source.File); errors.As(err, &notAStudyFile) {
		g.Errors.Write(response, http.StatusUnprocessableEntity,
			kind.unreadable+notAStudyFile.reason+".", request.requestID)
		return
	} else if err != nil {
		g.fail(ctx.Context(), response, request.requestID, "could not read the artifact's study file", err)
		return
	}

	var (
		answer   *pythonjson.Object
		refusal  *Refusal
		applyErr error
	)
	err = rows.ChangeStudy(ctx.Context(), request.caller.UserID, request.artifactID, kind.section, kind.holder,
		func(locked *LockedStudy) StudyWrite {
			// Deleted, or revised, between the read above and the lock. The
			// count just read from the file may be the old deck's, so
			// nothing computed from it may be written.
			if locked == nil || locked.Format != kind.format || locked.Generation != generation {
				refusal = &Refusal{Status: http.StatusConflict, Message: kind.revised}
				return StudyWrite{Abandon: true}
			}
			var next []byte
			next, answer, refusal, applyErr = apply(locked.States)
			if refusal != nil || applyErr != nil {
				return StudyWrite{Abandon: true}
			}
			return StudyWrite{States: next}
		})
	switch {
	case err != nil:
		g.fail(ctx.Context(), response, request.requestID, "could not store the study state", err)
	case applyErr != nil:
		g.fail(ctx.Context(), response, request.requestID, "could not render the study state", applyErr)
	case refusal != nil:
		g.Errors.Write(response, refusal.Status, refusal.Message, request.requestID)
	default:
		sendState(response, answer)
	}
}

// sendState answers with one person's state. It is theirs alone and changes
// with every card, so nothing may keep a copy.
func sendState(response http.ResponseWriter, state *pythonjson.Object) {
	body, err := pythonjson.Marshal(state)
	if err != nil {
		// A state is integers and a few fixed words; it cannot fail to
		// render. Kept as a plain 500 rather than a panic.
		http.Error(response, "internal error", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", privateNoStore)
	pythonjson.Send(response, http.StatusOK, body)
}

// pathOfArtifact is the two path parameters every route here declares.
type pathOfArtifact struct {
	ArtifactID string `path:"artifact_id" doc:"The artifact's id"`
}

// register adds one operation that is answered by `serve`.
func register(api huma.API, id, method, path, summary string, serve func(huma.Context)) {
	huma.Register(api, huma.Operation{
		OperationID: id,
		Method:      method,
		Path:        path,
		Summary:     summary,
		Tags:        []string{"artifacts"},
	}, func(context.Context, *pathOfArtifact) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: serve}, nil
	})
}

// FlashcardRoutes serves the three writes of a flashcard deck's study state:
// marking a card, forgetting every mark, and setting the order.
type FlashcardRoutes struct {
	Gate
	Study StudyRows
	// StudyFiles is the process's one reader of study files, shared with the
	// manifest and the quiz writes (study_cache.go).
	StudyFiles *StudyFiles
}

// Register adds the three operations.
func (r FlashcardRoutes) Register(api huma.API) {
	register(api, "mark-flashcard", http.MethodPatch, FlashcardProgressPath,
		"Mark one flashcard as known or to be seen again", r.mark)
	register(api, "reset-flashcard-progress", http.MethodDelete, FlashcardProgressPath,
		"Forget every flashcard mark", r.reset)
	register(api, "order-flashcards", http.MethodPut, FlashcardOrderPath,
		"Set the order a flashcard deck is studied in", r.reorder)
}

// change is the part the three flashcard writes share: read the deck for
// its size, then change the caller's state under the lock. `change` reports
// whether the new state is worth keeping; one that is not is removed.
func (r FlashcardRoutes) change(
	ctx huma.Context,
	response http.ResponseWriter,
	request admitted,
	generation int64,
	change func(state flashcardState, cards int) (next flashcardState, keep bool, refusal *Refusal),
) {
	cards := 0
	r.studyWrite(ctx, response, request, r.Study, flashcardKind, generation,
		func(file *StoredFile) (err error) {
			cards, err = r.StudyFiles.Deck(ctx.Context(), file)
			return err
		},
		func(stored []byte) ([]byte, *pythonjson.Object, *Refusal, error) {
			states := flashcardStates(stored, generation, cards)
			next, keep, refusal := change(stateOf(states, request.caller.UserID, generation, cards), cards)
			if refusal != nil {
				return nil, nil, refusal, nil
			}
			if keep {
				states[request.caller.UserID] = next
			} else {
				delete(states, request.caller.UserID)
			}
			encoded, err := writeStates(states, flashcardState.render)
			return encoded, next.render(), nil, err
		})
}

func (r FlashcardRoutes) mark(ctx huma.Context) {
	response := humawriter.New(ctx)
	var (
		generation, cardIndex int64
		mark                  *string
	)
	request, ok := r.admit(ctx, response,
		func(failures *[]errorenvelope.Failure) {
			body := readStudyBody(ctx, failures)
			generation = body.generation()
			// Bounded by the format, not by this deck: "past the end of
			// *this* deck" needs the deck, and is answered below.
			cardIndex = body.integer("card_index", 0, maxCards-1)
			mark = body.mark()
		})
	if !ok {
		return
	}
	r.change(ctx, response, request, generation,
		func(state flashcardState, cards int) (flashcardState, bool, *Refusal) {
			next, refusal := markFlashcard(state, cards, int(cardIndex), mark)
			return next, true, refusal
		})
}

func (r FlashcardRoutes) reorder(ctx huma.Context) {
	response := humawriter.New(ctx)
	var (
		generation int64
		order      []int
	)
	request, ok := r.admit(ctx, response,
		func(failures *[]errorenvelope.Failure) {
			body := readStudyBody(ctx, failures)
			generation = body.generation()
			order = body.order()
		})
	if !ok {
		return
	}
	r.change(ctx, response, request, generation,
		func(state flashcardState, cards int) (flashcardState, bool, *Refusal) {
			next, refusal := reorderFlashcards(state, cards, order)
			return next, true, refusal
		})
}

// reset takes its generation from the query string, because a DELETE has no
// body: `DELETE .../flashcard-progress?generation=3`.
func (r FlashcardRoutes) reset(ctx huma.Context) {
	response := humawriter.New(ctx)
	var generation int64
	request, ok := r.admit(ctx, response,
		func(failures *[]errorenvelope.Failure) {
			requested := ctx.URL()
			text, named := requestnumber.QueryValue(requested.Query(), "generation")
			if !named {
				*failures = append(*failures, errorenvelope.Failure{
					Location: []string{"query", "generation"}, Message: "Field required",
				})
				return
			}
			generation = readID(text, failures, "query", "generation")
		})
	if !ok {
		return
	}
	r.change(ctx, response, request, generation,
		func(state flashcardState, cards int) (flashcardState, bool, *Refusal) {
			next, keep := resetFlashcards(state, cards)
			return next, keep, nil
		})
}

// QuizRoutes serves the three writes of a quiz's state: answering a
// question, skipping one, and starting the next run.
type QuizRoutes struct {
	Gate
	Study      StudyRows
	StudyFiles *StudyFiles
}

// Register adds the three operations.
func (r QuizRoutes) Register(api huma.API) {
	register(api, "answer-quiz-question", http.MethodPut, QuizAnswerPath,
		"Answer one question of a quiz", r.answer)
	register(api, "skip-quiz-question", http.MethodPut, QuizSkipPath,
		"Skip one question of a quiz", r.skip)
	register(api, "retake-quiz", http.MethodPost, QuizRetakePath,
		"Start a quiz again, in full or only what was missed", r.retake)
}

// change is the part the three quiz writes share: read the quiz for its
// answer key, then change the caller's run under the lock.
func (r QuizRoutes) change(
	ctx huma.Context,
	response http.ResponseWriter,
	request admitted,
	generation int64,
	change func(state quizState, correct []int) (quizState, *Refusal),
) {
	var correct []int
	r.studyWrite(ctx, response, request, r.Study, quizKind, generation,
		func(file *StoredFile) (err error) {
			correct, err = r.StudyFiles.Quiz(ctx.Context(), file)
			return err
		},
		func(stored []byte) ([]byte, *pythonjson.Object, *Refusal, error) {
			states := quizStates(stored, generation, len(correct))
			next, refusal := change(runOf(states, request.caller.UserID, generation, len(correct)), correct)
			if refusal != nil {
				return nil, nil, refusal, nil
			}
			states[request.caller.UserID] = next
			encoded, err := writeStates(states, quizState.render)
			return encoded, next.render(), nil, err
		})
}

func (r QuizRoutes) answer(ctx huma.Context) {
	response := humawriter.New(ctx)
	var generation, questionIndex, option int64
	request, ok := r.admit(ctx, response,
		func(failures *[]errorenvelope.Failure) {
			body := readStudyBody(ctx, failures)
			generation = body.generation()
			questionIndex = body.integer("question_index", 0, maxQuestions-1)
			option = body.integer("selected_option_index", 0, optionsPerQuiz-1)
		})
	if !ok {
		return
	}
	r.change(ctx, response, request, generation,
		func(state quizState, _ []int) (quizState, *Refusal) {
			return answerQuestion(state, int(questionIndex), int(option))
		})
}

func (r QuizRoutes) skip(ctx huma.Context) {
	response := humawriter.New(ctx)
	var generation, questionIndex int64
	request, ok := r.admit(ctx, response,
		func(failures *[]errorenvelope.Failure) {
			body := readStudyBody(ctx, failures)
			generation = body.generation()
			questionIndex = body.integer("question_index", 0, maxQuestions-1)
		})
	if !ok {
		return
	}
	r.change(ctx, response, request, generation,
		func(state quizState, _ []int) (quizState, *Refusal) {
			return skipQuestion(state, int(questionIndex))
		})
}

func (r QuizRoutes) retake(ctx huma.Context) {
	response := humawriter.New(ctx)
	var (
		generation int64
		mode       string
	)
	request, ok := r.admit(ctx, response,
		func(failures *[]errorenvelope.Failure) {
			body := readStudyBody(ctx, failures)
			generation = body.generation()
			mode = body.mode()
		})
	if !ok {
		return
	}
	r.change(ctx, response, request, generation,
		func(state quizState, correct []int) (quizState, *Refusal) {
			return retakeQuiz(state, correct, mode)
		})
}
