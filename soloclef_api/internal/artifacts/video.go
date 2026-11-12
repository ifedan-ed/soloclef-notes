package artifacts

import (
	"net/http"
	"slices"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Video is the row the video route reads: the artifact and its document's
// title.
type Video struct {
	ID     int64
	Title  string
	Format string
	// Metadata is artifacts.metadata as the column's text, nil for NULL.
	Metadata []byte
	ThreadID *int64
}

// BuildVideo is the payload a video artifact is played from, or the 404 for
// an artifact that has none.
func BuildVideo(found Video) (*pythonjson.Object, *Refusal) {
	if found.Format != videoFormat {
		return nil, &Refusal{Status: http.StatusNotFound, Message: notAVideo}
	}
	// A video is playable once its metadata holds both lists. Metadata that
	// is not an object holds neither, which is "no payload", not a failure.
	meta := objectOf(decodeColumn(found.Metadata))
	stored, slidesAreAList := meta.GetOrNil("slides").([]any)
	sceneCodes, codesAreAList := meta.GetOrNil("scene_codes").([]any)
	if !slidesAreAList || !codesAreAList {
		return nil, &Refusal{Status: http.StatusNotFound, Message: payloadAbsent}
	}
	slides := slidesForPlayer(found.ID, stored)

	payload := pythonjson.NewObject()
	payload.Set("artifact_id", found.ID)
	payload.Set("title", found.Title)
	// An artifact that has a payload at all is ready to play.
	payload.Set("status", "ready")
	payload.Set("slides", slides)
	// The player's own code for each scene, passed through as stored.
	payload.Set("scene_codes", sceneCodes)
	// The number of slides sent. It used to count entries that were dropped
	// for not being slides, so a player could be told of a slide it was
	// never given.
	payload.Set("slide_count", len(slides))
	payload.Set("thread_id", optionalNumber(found.ThreadID))
	return payload, nil
}

// storageKeys are where a stored slide says its narration is: a key in file
// storage, a path on the server that rendered it, and which storage backend.
// None of them is the player's business and none is ever sent -- a browser is
// given `audio_url` instead, which goes back through this API and its gate.
//
// `audio_file` used to be sent whenever the slide also had a storage key,
// which put a server filesystem path in front of every viewer.
var storageKeys = []string{"audio_storage_key", "audio_file", "storage_backend"}

// slidesForPlayer is each stored slide with its storage details taken out
// and an `audio_url` put in. An entry that is not an object is not a slide
// and is left out.
func slidesForPlayer(artifactID int64, slides []any) []any {
	out := make([]any, 0, len(slides))
	for _, raw := range slides {
		stored, isObject := raw.(*pythonjson.Object)
		if !isObject {
			continue
		}
		slide := pythonjson.NewObject()
		for _, key := range stored.Keys() {
			if !slices.Contains(storageKeys, key) {
				slide.Set(key, stored.GetOrNil(key))
			}
		}
		// A slide has narration when it names any, and it can only be
		// fetched by a slide number, so both are needed for a URL.
		hasAudio := present(stored.GetOrNil("audio_storage_key")) || present(stored.GetOrNil("audio_file"))
		number, numbered := integer(stored.GetOrNil("slide_number"))
		if hasAudio && numbered {
			slide.Set("audio_url",
				"/api/v1/artifacts/"+strconv.FormatInt(artifactID, 10)+
					"/slides/"+string(number)+slideAudioSuffix)
		} else {
			slide.Set("audio_url", nil)
		}
		out = append(out, slide)
	}
	return out
}
