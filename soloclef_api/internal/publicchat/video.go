package publicchat

import (
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// BuildVideo is everything get_public_artifact_video does once it has the
// artifact: the 404 for a payload that is not there, and then the payload.
//
// absent is true for that 404. An error is something Python raises: metadata
// that is truthy and not an object, or a slide dict() cannot be made from.
func BuildVideo(token string, found VideoArtifact) (payload *pythonjson.Object, absent bool, err error) {
	metadata, err := decodeColumn(found.Metadata)
	if err != nil {
		return nil, false, err
	}
	// `meta = artifact.artifact_metadata or {}` and then `meta.get(...)`:
	// anything falsy is an empty dict, and anything truthy that is not an
	// object has no `.get`.
	meta := pythonjson.NewObject()
	if truthy(metadata) {
		object, isObject := metadata.(*pythonjson.Object)
		if !isObject {
			return nil, false, raised("artifact metadata is truthy and not an object")
		}
		meta = object
	}
	// Both must be lists. An empty list is one; an object or a string is not.
	slides, slidesAreAList := meta.GetOrNil("slides").([]any)
	sceneCodes, codesAreAList := meta.GetOrNil("scene_codes").([]any)
	if !slidesAreAList || !codesAreAList {
		return nil, true, nil
	}

	shared, err := publicSlides(token, found.ID, slides)
	if err != nil {
		return nil, false, err
	}

	payload = pythonjson.NewObject()
	payload.Set("artifact_id", found.ID)
	if found.Title == nil {
		payload.Set("title", nil)
	} else {
		payload.Set("title", *found.Title)
	}
	// A literal: an artifact that has a payload at all is "ready".
	payload.Set("status", "ready")
	payload.Set("slides", shared)
	// Passed through exactly as stored -- not one key is taken out of it.
	payload.Set("scene_codes", sceneCodes)
	payload.Set("slide_count", len(slides))
	return payload, false, nil
}

// publicSlides is _public_artifact_slides: each stored slide with three keys
// taken out and a share-scoped `audio_url` put in.
//
// It is not internal/artifacts' slidesForRemotion with a different URL. The
// signed-in route skips an entry that is not an object; this one calls
// `dict(raw)` on every entry without asking, so an entry that is not an
// object is either made into a dict by Python's own rules (dictOf) or is the
// 500 those rules raise -- and one bad entry fails the whole answer.
func publicSlides(token string, artifactID int64, slides []any) ([]any, error) {
	shared := make([]any, 0, len(slides))
	for _, raw := range slides {
		slide, err := dictOf(raw)
		if err != nil {
			return nil, err
		}
		slideNumber := slide.Get("slide_number")

		// has_audio = bool(slide.pop("audio_storage_key", None)
		//                  or slide.pop("audio_file", None))
		//
		// The `or` evaluates its right side only when the left is falsy. So
		// a slide with a truthy storage key never has `audio_file` popped,
		// and that key -- a path on the server's own disk -- is still in the
		// slide that is sent to whoever holds the link. It is what the
		// Python route answers and it is pinned in disclosure_test.go.
		hasAudio := truthy(slide.Pop("audio_storage_key"))
		if !hasAudio {
			hasAudio = truthy(slide.Pop("audio_file"))
		}
		slide.Pop("storage_backend")

		// Assigning to a key replaces a stored `audio_url` where it stands
		// and appends it otherwise.
		if _, isInt := pythonInt(slideNumber); hasAudio && isInt {
			// The token goes in as the route received it, unquoted: this is
			// an f-string, not a URL builder.
			slide.Set("audio_url", publicPrefix+token+
				"/artifacts/"+strconv.FormatInt(artifactID, 10)+
				"/slides/"+formattedInt(slideNumber)+"/audio")
		} else {
			slide.Set("audio_url", nil)
		}
		shared = append(shared, slide)
	}
	return shared, nil
}
