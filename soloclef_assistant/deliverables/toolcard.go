package deliverables

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
)

// What a card receives is not only shown. It is saved in the message,
// synced to every open page of the thread, and published if the thread is
// shared. So each card here is an allowlist: the result is decoded into a
// type holding exactly the fields the page's card draws and encoded again,
// and a field a tool one day adds to its result does not reach the browser
// until it is added to that type on purpose.

// PodcastCard is what the page's podcast card (components/tool-ui/podcast)
// draws from. With a podcast id it follows the podcast live and needs
// nothing else; without one it shows Error.
type PodcastCard struct {
	Status    string `json:"status"`
	PodcastID int64  `json:"podcast_id,omitempty"`
	Title     string `json:"title,omitempty"`
	Error     string `json:"error,omitempty"`
}

// statusFailed is the status the podcast card shows its error state for.
const statusFailed = "failed"

// maxCardErrorChars bounds the sentence a failed podcast's card carries.
const maxCardErrorChars = 300

// ToolCard is the card payload for a finished call of one of this package's
// tools, and whether the result is one this package has a card for. With
// false the caller shows its default card, which says nothing of the
// result.
//
// It is named ToolCard because Card is a flashcard.
func ToolCard(result event.Event) (payload any, ok bool) {
	switch result.Tool {
	case SaveTool:
		// A save that failed has no card: the page draws nothing for it,
		// and the specialist's answer says what happened.
		if result.IsError {
			return nil, false
		}
		var saved Saved
		if json.Unmarshal([]byte(result.Text), &saved) != nil || saved.Status != statusSaved || saved.ArtifactID <= 0 {
			return nil, false
		}
		if saved.Files == nil {
			// The page reads "files" as a list or as absent, never null.
			saved.Files = []SavedFile{}
		}
		return saved, true
	case VerifyTool:
		// The page's verification card (components/tool-ui/verify-artifact)
		// draws the verdict, the findings and the page count. A call that
		// failed outright has no verdict and shows as the default card.
		if result.IsError {
			return nil, false
		}
		if card, drawn := verifyCard(result.Text); drawn {
			return card, true
		}
		return nil, false
	case PodcastTool:
		if result.IsError {
			// Without this the page would show the default card's
			// "completed" as a podcast from an older version. The text is
			// a sentence written by this package or by the agent loop,
			// never a storage error: podcastTool replaces those.
			reason := strings.TrimSuffix(strings.TrimPrefix(result.Text, "error: "), refusedPodcastNote)
			if utf8.RuneCountInString(reason) > maxCardErrorChars {
				reason = string([]rune(reason)[:maxCardErrorChars])
			}
			return PodcastCard{Status: statusFailed, Error: reason}, true
		}
		var opened PodcastOpened
		if json.Unmarshal([]byte(result.Text), &opened) != nil || opened.PodcastID <= 0 || opened.Status == "" {
			return nil, false
		}
		// The message is the model's; the card has no use for it.
		return PodcastCard{Status: opened.Status, PodcastID: opened.PodcastID, Title: opened.Title}, true
	case ImageTool:
		return imageCard(result)
	case VideoTool:
		return videoCard(result)
	}
	return nil, false
}
