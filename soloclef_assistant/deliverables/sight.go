package deliverables

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/accountvision"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
)

// Sight is the vision model that looks at an account's pictures, opened
// for the account (accountvision.Models): its own choice, a proxy model or
// a model of its own connection, else the deployment's VisionModel. It
// reads an infographic back (VisionImageReader), so that check is made by
// the model the account chose, as its uploads' pictures are described.
type Sight interface {
	For(ctx context.Context, userID string) (vision.Endpoint, error)
}

// errNoSight is an account with no vision model and a deployment with
// none (accountvision.ErrNone); each check says it in its own words.
var errNoSight = errors.New("deliverables: no vision model is set for this account or on this server")

// SightUnavailable is the account's chosen vision model that cannot be
// called (removed, switched off, its key unreadable), with the sentence
// for it: the check is not made, and says why in the person's words.
type SightUnavailable struct {
	Sentence string
	Cause    error
}

func (s *SightUnavailable) Error() string { return s.Sentence }
func (s *SightUnavailable) Unwrap() error { return s.Cause }

// openSight is the account's vision model, or errNoSight, or a
// *SightUnavailable.
func openSight(ctx context.Context, sight Sight, userID string) (vision.Endpoint, error) {
	endpoint, err := sight.For(ctx, userID)
	if errors.Is(err, accountvision.ErrNone) {
		return vision.Endpoint{}, errNoSight
	}
	if sentence, said := accountvision.Sentence(err); said {
		return vision.Endpoint{}, &SightUnavailable{Sentence: sentence, Cause: err}
	}
	return endpoint, err
}

// sightPart is one part of a message shown to a vision model.
type sightPart struct {
	Type     string     `json:"type"`
	Text     string     `json:"text,omitempty"`
	ImageURL *sightURLs `json:"image_url,omitempty"`
}

type sightURLs struct {
	URL string `json:"url"`
}

// pictureOf is a picture as a message part, inline.
func pictureOf(mimeType string, data []byte) sightPart {
	return sightPart{Type: "image_url", ImageURL: &sightURLs{URL: "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)}}
}

// askSight shows the endpoint's model one user message and returns what it
// wrote. what names the model in an error. A guard's refusal comes back as
// the *vision.Refusal it is.
func askSight(ctx context.Context, endpoint vision.Endpoint, content []sightPart, limit int64, what string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model":    endpoint.Model,
		"messages": []map[string]any{{"role": "user", "content": content}},
	})
	if err != nil {
		return "", err
	}
	response, err := endpoint.Post(ctx, body)
	if err != nil {
		return "", err
	}
	raw, err := answerOf(response, "the vision model", limit)
	if err != nil {
		return "", err
	}
	var answer struct {
		Choices []struct {
			Message struct {
				// A string, or a reasoning model's list of parts.
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || len(answer.Choices) == 0 {
		return "", fmt.Errorf("%s answered nothing readable", what)
	}
	return vision.ReplyText(answer.Choices[0].Message.Content), nil
}
