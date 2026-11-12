package podcasts

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/voices"
)

// BriefRoute serves PATCH /api/v1/podcasts/{podcast_id}/spec: the brief,
// replaced with the one the user edited at the gate.
//
// An edit is accepted only when four things hold, checked in this order:
//
//  1. the body is a whole brief (422, listing every fault);
//  2. the podcast is the caller's own (404 otherwise);
//  3. the podcast is at the brief gate, and at the version the editor was
//     looking at (409 -- the web app reloads on a 409);
//  4. the roster has every voice the brief names, and each speaks the
//     brief's language (422, at the speaker).
//
// and it is written only if 3 still holds at the moment of writing
// (queries.sql, ReplaceBrief).
//
// The fourth is new. The brief is the last thing a person looks at before
// the transcript is drafted and voiced, and a voice the service cannot speak
// used to pass every check up to the render, which failed after the draft
// had been paid for.
//
// The body is read by the tier's body reader before the route runs, so the
// pair is listed in cmd/api's `withBodies`.
type BriefRoute struct {
	Podcasts Database
	Voices   Catalogue
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
}

func (r BriefRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "update-podcast-brief",
		Method:      http.MethodPatch,
		Path:        BriefPath,
		Summary:     "Replace the brief while it is being reviewed",
		Tags:        []string{"podcasts"},
	}, func(ctx context.Context, _ *struct {
		PodcastID string `path:"podcast_id" doc:"The podcast's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r BriefRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	answer := answerer{
		ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestcontext.ID(ctx.Context()),
		what:      "the podcast's brief could not be edited",
	}
	caller, id, proceed := answer.start()
	if !proceed {
		return
	}
	body, failures, err := decodeBody(ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if err != nil {
		answer.fail("a body reached the route that the body reader should have turned away", err)
		return
	}
	var edit briefEdit
	if failures == nil {
		edit, failures = readBriefEdit(body)
	}
	if len(failures) > 0 {
		answer.errors.WriteValidation(response, answer.requestID, failures)
		return
	}

	// Asked before the transaction, because it can mean a request to the
	// model proxy and a transaction must not be held open across one. A
	// failure is kept and reported only once the podcast is known to be the
	// caller's and at the gate: a 404 or a 409 is the truer answer.
	roster, rosterErr := r.Voices.Roster(ctx.Context())

	var podcast *Stored
	err = r.Podcasts.Write(ctx.Context(), func(ctx context.Context, rows Writing) error {
		loaded, err := load(ctx, rows, caller, id)
		if err != nil {
			return err
		}
		if loaded.Status != statusAwaitingBrief {
			return conflict(notAtTheGate + loaded.Status)
		}
		if edit.ExpectedVersion != loaded.SpecVersion {
			return conflict("spec version conflict: expected " + strconv.FormatInt(edit.ExpectedVersion, 10) +
				", current is " + strconv.FormatInt(loaded.SpecVersion, 10))
		}
		if rosterErr != nil {
			return rosterErr
		}
		if unspeakable := voiceFaults(roster, edit.Brief); len(unspeakable) > 0 {
			return &invalid{failures: unspeakable}
		}
		keepDeliveryNotes(edit.Brief, loaded.Spec)

		spec, err := pythonjson.Marshal(edit.Brief.rendered)
		if err != nil {
			return err
		}
		replaced, err := rows.ReplaceBrief(ctx, caller.UserID, loaded.ID, edit.ExpectedVersion, spec)
		if err != nil {
			return err
		}
		if !replaced {
			// Another edit, an approval or a delete got there between the
			// read and the write.
			return conflict(changedMeanwhile)
		}
		// The row as it now is, read back through the same decoder a stored
		// column goes through, so the answer is rendered exactly as the
		// next read of this podcast will be.
		if loaded.Spec, err = decodeColumn(spec); err != nil {
			return err
		}
		loaded.SpecVersion++
		podcast = loaded
		return nil
	})
	if err != nil {
		answer.refuse(err, "could not replace the brief")
		return
	}

	rendered, err := detail(ctx.Context(), r.Logger, podcast)
	if err != nil {
		answer.fail("the brief is replaced and its answer could not be rendered", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, rendered)
}

// voiceFaults is what the roster has to say about a brief: a language no
// voice speaks, a voice the service does not have, a voice that does not
// speak the language. Each is located at the field it is about, under
// `body.spec`, so the form can mark it.
//
// Two speakers may share a voice. It is rarely what is wanted, but it is
// what a proposal itself does when a language has fewer voices than the
// podcast has speakers, and the user is looking straight at it.
func voiceFaults(roster *voices.Roster, brief readBrief) []errorenvelope.Failure {
	var wrong faults
	at := []string{requestbody.BodyRoot, "spec"}
	if !roster.Supports(brief.language) {
		wrong.add("The speech service has no voice for "+brief.language, at, "language")
		// Every voice would then be reported as not speaking it, which
		// says the same thing once per speaker.
		return wrong.found
	}
	for index, speaker := range brief.speakers {
		voice, found := roster.Voice(speaker.voice)
		switch {
		case !found:
			wrong.add(voiceAbsent, at, "speakers", strconv.Itoa(index), "voice_id")
		case !voice.Speaks(brief.language):
			wrong.add("This voice does not speak "+brief.language, at, "speakers", strconv.Itoa(index), "voice_id")
		}
	}
	return wrong.found
}

// keepDeliveryNotes carries a speaker's delivery note over from the stored
// brief when the edit does not mention one.
//
// A delivery note says how a speaker should sound ("warm and unhurried"),
// and is written by voice casting, not by a person. The web app's form does
// not know the field, so what it sends back has no `delivery` key at all,
// and replacing the brief wholesale erased every note on each save. So:
//
//   - a speaker whose edit names `delivery`, null included, gets what it
//     names;
//   - a speaker whose edit does not, and who is the same slot with the same
//     voice as before, keeps the note they had;
//   - a speaker whose voice changed gets none: the note was written for the
//     voice.
func keepDeliveryNotes(edit readBrief, storedColumn any) {
	if storedColumn == nil {
		return
	}
	before, wrong := parseBrief(storedColumn, stored, nil)
	if len(wrong) > 0 {
		// The stored brief cannot be read, which is a fine reason to be
		// replacing it. There is nothing to carry over.
		return
	}
	for _, speaker := range edit.speakers {
		if speaker.deliveryNamed {
			continue
		}
		for _, earlier := range before.speakers {
			if earlier.slot == speaker.slot && earlier.voice == speaker.voice {
				speaker.rendered.Set("delivery", earlier.rendered.GetOrNil("delivery"))
			}
		}
	}
}
