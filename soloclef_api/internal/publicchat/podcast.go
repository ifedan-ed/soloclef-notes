package publicchat

import (
	"math/big"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// FindPodcast is get_snapshot_podcast once it has the snapshot's data: the
// first entry of `podcasts` whose `original_id` equals the id asked for, or
// nil when there is none.
//
// The loop is `for podcast in podcasts: if podcast.get("original_id") ==
// podcast_id: return podcast`, over whatever `snapshot_data.get("podcasts",
// [])` is, and it stops at the first match. So:
//
//   - a missing key is an empty list, but a key holding null is None, and
//     iterating None is a TypeError;
//   - an entry that is not an object is an AttributeError -- but only if the
//     loop reaches it, so a bad entry *after* the match is never noticed;
//   - a non-empty string is iterated by character and a non-empty object by
//     key, and neither a character nor a key has a `.get`;
//   - the comparison is Python's `==`, so an id stored as 3.0 or as true is
//     found by 3 and by 1, and one stored as "3" is not (equalsInt).
func FindPodcast(data *pythonjson.Object, podcastID *big.Int) (*pythonjson.Object, error) {
	podcasts, present := data.Get("podcasts")
	if !present {
		return nil, nil
	}
	entries, err := iterated(podcasts)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		podcast, isObject := entry.(*pythonjson.Object)
		if !isObject {
			return nil, raised("a podcasts entry is not an object, and the loop calls .get on it")
		}
		if equalsInt(podcast.GetOrNil("original_id"), podcastID) {
			return podcast, nil
		}
	}
	return nil, nil
}

// iterated is what a Python `for` loop walks when it is given a value
// json.loads produced: a list's items, a string's characters, an object's
// keys, and a TypeError for anything else.
func iterated(value any) ([]any, error) {
	switch held := value.(type) {
	case []any:
		return held, nil
	case string:
		characters := make([]any, 0, len(held))
		for _, character := range held {
			characters = append(characters, string(character))
		}
		return characters, nil
	case *pythonjson.Object:
		keys := make([]any, 0, held.Len())
		for _, key := range held.Keys() {
			keys = append(keys, key)
		}
		return keys, nil
	}
	return nil, raised("a for loop over a value that is not iterable")
}

// BuildPodcast is the dict get_public_podcast returns for an entry it found.
//
// Four keys, three of them read off the entry with `.get`, so each is null
// when the entry does not have it. `id` is the entry's own `original_id`
// exactly as stored -- 3.0 stays 3.0 and true stays true -- rather than the
// integer from the path. `status` is a literal: an entry that is in a
// snapshot at all is "ready", whatever it says about itself.
//
// Nothing else of the entry is sent. A snapshot taken before audio moved into
// artifact storage carries `storage_key` and `file_path` here, and a later
// one carries the ids of the rows it was made from; none of them is in the
// answer. What *is* sent is sent whole: `title` and `transcript` are not
// passed through app/public_sharing/redaction.py, so a key inside them that
// the page route would strip is kept (disclosure_test.go).
func BuildPodcast(podcast *pythonjson.Object) *pythonjson.Object {
	answer := pythonjson.NewObject()
	answer.Set("id", podcast.GetOrNil("original_id"))
	answer.Set("title", podcast.GetOrNil("title"))
	answer.Set("status", "ready")
	answer.Set("podcast_transcript", podcast.GetOrNil("transcript"))
	return answer
}
