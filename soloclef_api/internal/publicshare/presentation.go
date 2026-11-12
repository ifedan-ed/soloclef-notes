package publicshare

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// This file is the video presentations frozen whole into a link, which is
// how a presentation was shared before a video became an artifact. No link
// made today carries one (a video card names its artifact, and
// internal/publicchat serves that), but a card in an older link still names
// a presentation id, and its player asks here.

// internalKeys never leave in an answer, wherever they appear inside a
// frozen slide: where a file is stored and who an account is. The list is
// the one a link is cleaned with when it is made (internal/chat/threads);
// it is applied again here because these entries were frozen before that
// cleaning covered them.
var internalKeys = map[string]bool{
	"storage_key": true, "audio_storage_key": true, "storage_backend": true,
	"file_path": true, "audio_file": true,
	"user_id": true, "created_by_id": true, "author_id": true,
	"email": true, "user_email": true,
	"api_key": true, "token": true,
}

// withoutInternalKeys is value with every internal key removed, at any
// depth.
func withoutInternalKeys(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if !internalKeys[key] {
				out[key] = withoutInternalKeys(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = withoutInternalKeys(item)
		}
		return out
	}
	return value
}

// frozenPresentation is the presentation with this id in a live link, or
// false with the 404 answered.
func (x *exchange) frozenPresentation(id int64) (map[string]any, bool) {
	frozen, err := New(x.routes.Pool).FrozenMedia(x.ctx.Context(), FrozenMediaParams{
		ShareToken: x.ctx.Param("share_token"), Now: x.routes.now()})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		x.fail("could not read a link's presentations", err)
		return nil, false
	}
	found := numbered(entries(frozen.Presentations), "original_id", id)
	if found == nil {
		x.refuse(http.StatusNotFound, presentationAbsent)
		return nil, false
	}
	return found, true
}

// slidesOf is a frozen presentation's slides: the objects of its list.
func slidesOf(presentation map[string]any) []map[string]any {
	list, _ := presentation["slides"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if slide, isObject := item.(map[string]any); isObject {
			out = append(out, slide)
		}
	}
	return out
}

// presentation is the payload a frozen presentation's player draws from:
// its slides and their scene code. A slide is given an audio address only
// when its narration is in the file store, which is the only narration
// this tier serves (sendSlideAudio); the others play silent, where the
// Python sent an address that answered 404 once the rendering machine's
// disk was gone.
func (r Routes) presentation(x *exchange) {
	ids, ok := x.ids("video_presentation_id")
	if !ok {
		return
	}
	found, ok := x.frozenPresentation(ids[0])
	if !ok {
		return
	}
	stored := slidesOf(found)
	slides := make([]any, 0, len(stored))
	for _, slide := range stored {
		public, _ := withoutInternalKeys(slide).(map[string]any)
		public["audio_url"] = nil
		key, _ := slide["audio_storage_key"].(string)
		if number, numbered := wholeNumber(slide["slide_number"]); numbered && strings.TrimSpace(key) != "" {
			public["audio_url"] = "/api/v1/public/" + url.PathEscape(x.ctx.Param("share_token")) +
				"/video-presentations/" + strconv.FormatInt(ids[0], 10) +
				"/slides/" + strconv.FormatInt(number, 10) + "/audio"
		}
		slides = append(slides, public)
	}
	x.send(http.StatusOK, map[string]any{
		"id":          ids[0],
		"title":       found["title"],
		"status":      "ready",
		"slides":      slides,
		"scene_codes": withoutInternalKeys(found["scene_codes"]),
		"slide_count": len(slides),
	})
}

// presentationAudio is one slide's narration of a frozen presentation.
func (r Routes) presentationAudio(x *exchange) {
	ids, ok := x.ids("video_presentation_id", "slide_number")
	if !ok {
		return
	}
	found, ok := x.frozenPresentation(ids[0])
	if !ok {
		return
	}
	x.sendSlideAudio(slidesOf(found), ids[1])
}
