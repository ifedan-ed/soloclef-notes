package podcasts

import (
	"context"
	"log/slog"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// detail is a podcast as every route here but the delete answers it: the
// PodcastDetail of app/podcasts/api/schemas.py, which the web app parses with
// `podcastDetail` (contracts/types/podcast.types.ts). The field names and
// their order are that contract.
//
// A brief or a transcript that is stored and cannot be read is answered as
// null and logged. The alternative is failing the whole answer, which hides a
// podcast's status, error and audio behind a fault in a column the caller may
// not even be looking at.
func detail(ctx context.Context, logger *slog.Logger, podcast *Stored) ([]byte, error) {
	spec, readable := readSpec(podcast.Spec)
	if !readable {
		logger.LogAttrs(ctx, slog.LevelWarn, "a podcast's stored brief cannot be read",
			slog.Int64("podcast_id", podcast.ID))
	}
	transcript, readable := readTranscript(podcast.Transcript)
	if !readable {
		logger.LogAttrs(ctx, slog.LevelWarn, "a podcast's stored transcript cannot be read",
			slog.Int64("podcast_id", podcast.ID))
	}

	body := pythonjson.NewObject()
	body.Set("id", podcast.ID)
	body.Set("title", podcast.Title)
	body.Set("status", podcast.Status)
	body.Set("spec_version", podcast.SpecVersion)
	body.Set("spec", orNull(spec))
	body.Set("transcript", orNull(transcript))
	body.Set("has_audio", podcast.ArtifactID != nil)
	body.Set("duration_seconds", numberOrNull(podcast.DurationSeconds))
	if podcast.Error == nil {
		body.Set("error", nil)
	} else {
		body.Set("error", *podcast.Error)
	}
	// RFC 3339 in UTC with a "Z", which is what the web app has always been
	// sent for this field.
	body.Set("created_at", pythonjson.PydanticDateTime(podcast.CreatedAt))
	body.Set("thread_id", numberOrNull(podcast.ThreadID))
	body.Set("artifact_id", numberOrNull(podcast.ArtifactID))
	return pythonjson.Marshal(body)
}

// orNull keeps a nil *Object from being stored as a non-nil interface.
func orNull(object *pythonjson.Object) any {
	if object == nil {
		return nil
	}
	return object
}

func numberOrNull(number *int64) any {
	if number == nil {
		return nil
	}
	return *number
}
