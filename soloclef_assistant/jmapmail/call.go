package jmapmail

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
)

// invocation is one method call of a request: RFC 8620 3.2's
// [name, arguments, call id] triple.
type invocation struct {
	name string
	args any
	id   string
}

func (i invocation) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{i.name, i.args, i.id})
}

// reply is one method response: [name, arguments, call id].
type reply struct {
	name string
	args json.RawMessage
	id   string
}

func (r *reply) UnmarshalJSON(raw []byte) error {
	var triple []json.RawMessage
	if err := json.Unmarshal(raw, &triple); err != nil || len(triple) != 3 {
		return errBroken
	}
	if json.Unmarshal(triple[0], &r.name) != nil || json.Unmarshal(triple[2], &r.id) != nil {
		return errBroken
	}
	r.args = triple[1]
	return nil
}

// methodError is RFC 8620 3.6.2's method-level error.
type methodError struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// replies are a request's responses, by call id.
type replies map[string]reply

// decode reads the response to call id into out. A method-level error is a
// *Failure in words for the person; a missing response is a broken answer.
func (r replies) decode(id, name string, out any) error {
	got, present := r[id]
	if !present {
		return errBroken
	}
	if got.name == "error" {
		var said methodError
		_ = json.Unmarshal(got.args, &said)
		return refusal(said.Type, said.Description)
	}
	if got.name != name || json.Unmarshal(got.args, out) != nil {
		return errBroken
	}
	return nil
}

// refusal is a server's error in words for the person: the known types in
// this package's words, any other as its type with the server's own
// description bounded and stripped of control characters (it is shown,
// and it is somebody else's text).
func refusal(kind, description string) *Failure {
	switch kind {
	case "forbidden":
		return &Failure{Message: "The mail server does not allow this for this sign-in"}
	case "unknownMethod", "unknownCapability", "accountNotSupportedByMethod":
		return &Failure{Message: "The mail server does not offer this to this sign-in (for an OAuth sign-in, the mail scope is needed)"}
	case "accountNotFound":
		return errNoMail
	case "accountReadOnly":
		return &Failure{Message: "This mail account is read-only on the server"}
	case "overQuota":
		return &Failure{Message: "The mailbox is over its quota on the server"}
	case "tooLarge":
		return &Failure{Message: "The message is larger than the server accepts"}
	case "rateLimit":
		return &Failure{Message: "The mail server is limiting how fast this sign-in may send; try again later"}
	}
	text := plainText(description, 200)
	if text == "" {
		return &Failure{Message: "The mail server refused the request (" + plainText(kind, 40) + ")"}
	}
	return &Failure{Message: "The mail server refused the request: " + text}
}

// plainText is text from a server made safe to show: control characters
// dropped, whitespace runs folded, cut at most runes.
func plainText(text string, most int) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if runes := []rune(cleaned); len(runes) > most {
		cleaned = string(runes[:most]) + "…"
	}
	return cleaned
}

// call posts one request of method calls to the session's API and returns
// the responses by call id.
func (c Client) call(ctx context.Context, session Session, credential Credential, using []string, calls ...invocation) (replies, error) {
	body, err := json.Marshal(struct {
		Using       []string     `json:"using"`
		MethodCalls []invocation `json:"methodCalls"`
	}{using, calls})
	if err != nil {
		return nil, err
	}
	status, _, raw, err := c.exchange(ctx, session.Origin, credential, http.MethodPost, session.APIURL, body, mostReplyBytes)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		// A request-level error (RFC 8620 3.6.1) is a problem document;
		// its type says what was wrong with the request as a whole.
		var problem struct {
			Type   string `json:"type"`
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(raw, &problem)
		if problem.Type != "" {
			return nil, refusal(strings.TrimPrefix(problem.Type, "urn:ietf:params:jmap:error:"), problem.Detail)
		}
		return nil, errBroken
	}
	var envelope struct {
		MethodResponses []reply `json:"methodResponses"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, errBroken
	}
	out := make(replies, len(envelope.MethodResponses))
	for _, response := range envelope.MethodResponses {
		// A method that answers more than once under one id (Email/set's
		// implicit update after EmailSubmission/set) keeps its first answer
		// under the id and the rest under the name, where a caller that
		// wants them looks.
		if _, seen := out[response.id]; seen {
			out[response.id+"/"+response.name] = response
			continue
		}
		out[response.id] = response
	}
	return out, nil
}

// backReference is RFC 8620 3.7's ResultReference: an argument taken from
// an earlier call's response in the same request.
type backReference struct {
	ResultOf string `json:"resultOf"`
	Name     string `json:"name"`
	Path     string `json:"path"`
}

// mailUsing is the capabilities a mail request declares.
var mailUsing = []string{capabilityCore, capabilityMail}
