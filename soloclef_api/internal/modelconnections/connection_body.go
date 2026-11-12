package modelconnections

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// The bounds of a connection's fields. The first two are the columns'; a
// key is far shorter than its bound, which exists so that a megabyte is
// not sealed and stored; a provider lists hundreds of models, not
// thousands.
const (
	maxProviderChars = 100
	maxBaseURLChars  = 500
	maxKeyChars      = 4096
	maxModelIDChars  = 255
	maxPickedModels  = 2000
)

// change is the fields of a connection a body named. Each field has a flag
// because a body reads the same way for a create, an update and a test of
// an update: a field that is present is the value, null included, and a
// field that is absent is not being changed.
type change struct {
	provider    string
	setProvider bool
	// baseURL and key are "" for null: no address, no key.
	baseURL    string
	setBaseURL bool
	key        string
	setKey     bool
	// extra is a JSON object, compact.
	extra      []byte
	setExtra   bool
	enabled    bool
	setEnabled bool
	// headers are the connection's own request headers (an
	// OpenAI-compatible endpoint's only). nil for null: no headers.
	headers    map[string]string
	setHeaders bool
}

// body is a JSON object's fields as they were sent.
type body map[string]json.RawMessage

func invalid(field, message string) errorenvelope.Failure {
	return errorenvelope.Failure{Location: []string{"body", field}, Message: message}
}

// readBody is the request's JSON object, or the refusal of anything else.
func readBody(raw []byte) (body, []errorenvelope.Failure) {
	var fields body
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, []errorenvelope.Failure{{Location: []string{"body"}, Message: "The body must be a JSON object"}}
	}
	return fields, nil
}

// text reads a field that is a string or null. present is false when the
// body did not name it; null reads as "".
func (b body) text(field string, maxChars int, failures *[]errorenvelope.Failure) (value string, present bool) {
	raw, named := b[field]
	if !named {
		return "", false
	}
	if string(raw) == "null" {
		return "", true
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		*failures = append(*failures, invalid(field, field+" must be text or null"))
		return "", true
	}
	if utf8.RuneCountInString(value) > maxChars {
		*failures = append(*failures, invalid(field, field+" is too long"))
	}
	return value, true
}

// readChange reads the connection fields of a body. creating says the body
// is a whole new connection: a provider is then required, and the fields
// left out take the values a new connection starts with.
func readChange(b body, creating bool) (change, []errorenvelope.Failure) {
	var (
		read     change
		failures []errorenvelope.Failure
	)

	read.provider, read.setProvider = b.text("provider", maxProviderChars, &failures)
	read.provider = strings.TrimSpace(read.provider)
	switch {
	case creating && !read.setProvider:
		failures = append(failures, invalid("provider", "provider is required"))
	case read.setProvider && !knownProvider(read.provider):
		// Null included: a connection always has a provider.
		failures = append(failures, invalid("provider", "provider must be one of the providers this server lists"))
	}

	read.baseURL, read.setBaseURL = b.text("base_url", maxBaseURLChars, &failures)
	read.baseURL = strings.TrimSpace(read.baseURL)
	if read.baseURL != "" {
		// The proxy is handed this address to call. Anything but a plain
		// web address is refused here, not discovered by a failed test.
		address, err := url.Parse(read.baseURL)
		if err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" || address.User != nil {
			failures = append(failures, invalid("base_url", "base_url must be an http or https address"))
		}
	}

	read.key, read.setKey = b.text("api_key", maxKeyChars, &failures)

	if raw, named := b["extra"]; named {
		read.setExtra = true
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil || (object == nil && !creating) {
			failures = append(failures, invalid("extra", "extra must be an object"))
		} else if namesHeaders(object) {
			// The headers are sealed by this tier and set through their
			// own field; a body that wrote these keys could store a value
			// in the clear or a list of names that is not true.
			failures = append(failures, invalid("extra", "extra may not set headers_sealed or header_names: send the headers as headers"))
		} else if object == nil {
			read.extra = []byte(`{}`)
		} else {
			var compact bytes.Buffer
			_ = json.Compact(&compact, raw)
			read.extra = compact.Bytes()
		}
	}

	if raw, named := b["enabled"]; named {
		read.setEnabled = true
		if err := json.Unmarshal(raw, &read.enabled); err != nil || string(raw) == "null" {
			failures = append(failures, invalid("enabled", "enabled must be true or false"))
		}
	}

	if raw, named := b["headers"]; named {
		read.setHeaders = true
		if err := json.Unmarshal(raw, &read.headers); err != nil {
			failures = append(failures, invalid("headers", "headers must be an object of header names and text values, or null"))
		} else if sentence := ownmodels.CheckHeaders(read.headers); sentence != "" {
			failures = append(failures, invalid("headers", sentence))
		}
	}

	if creating {
		// The one scope a person can make. The operator's connections are
		// the proxy's own configuration and are not made through a route.
		if raw, named := b["scope"]; named && string(raw) != `"user"` {
			failures = append(failures, invalid("scope", `scope must be "user": the server's own connections are configured in the model proxy`))
		}
		if !read.setExtra {
			read.extra = []byte(`{}`)
		}
		if !read.setEnabled {
			read.enabled = true
		}
	}
	return read, failures
}

// providerRules are what a connection of one provider must hold beyond
// readChange's shape: an OpenAI-compatible endpoint is called by this tier
// itself, so it needs an address the egress guard accepts and a key, and
// it alone sends headers of its own. creating says the body is a whole new
// connection; for a change, only the fields it names are held, plus both
// address and key when it moves a connection to that provider.
func providerRules(provider string, fields change, creating, changingProvider bool) []errorenvelope.Failure {
	var failures []errorenvelope.Failure
	if provider != ownmodels.Provider {
		if fields.setHeaders && len(fields.headers) > 0 {
			failures = append(failures, invalid("headers", "Headers are sent only to an OpenAI-compatible endpoint"))
		}
		return failures
	}
	whole := creating || changingProvider
	if whole || fields.setBaseURL {
		if _, err := ownmodels.CheckAddress(fields.baseURL); err != nil {
			failures = append(failures, invalid("base_url", ownmodels.RefusalSentence(err, fields.baseURL)))
		}
	}
	if (whole && !fields.setKey) || (fields.setKey && fields.key == "") {
		failures = append(failures, invalid("api_key", "An OpenAI-compatible connection needs the endpoint's API key"))
	}
	return failures
}

// namesHeaders reports whether an `extra` object names a key the headers
// are kept under.
func namesHeaders(object map[string]json.RawMessage) bool {
	for _, key := range ownmodels.ExtraKeys {
		if _, named := object[key]; named {
			return true
		}
	}
	return false
}

// pickedModel is one model chosen with a new connection, with the field
// names AddModels' jsonb_to_recordset reads.
type pickedModel struct {
	ModelID                 string          `json:"model_id"`
	DisplayName             *string         `json:"display_name"`
	Source                  string          `json:"source"`
	SupportsChat            *bool           `json:"supports_chat"`
	MaxInputTokens          *int32          `json:"max_input_tokens"`
	SupportsImageInput      *bool           `json:"supports_image_input"`
	SupportsTools           *bool           `json:"supports_tools"`
	SupportsImageGeneration *bool           `json:"supports_image_generation"`
	Enabled                 bool            `json:"enabled"`
	Catalog                 json.RawMessage `json:"catalog"`
}

// readPicked reads a new connection's `models`. A model id given twice is
// kept once, the first time; one that is blank is a mistake and refused,
// where the Python route dropped it without a word.
func readPicked(b body) ([]pickedModel, []errorenvelope.Failure) {
	raw, named := b["models"]
	if !named || string(raw) == "null" {
		return nil, nil
	}
	var sent []struct {
		pickedModel
		// The apps call a model's catalogue entry `metadata`.
		Metadata json.RawMessage `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &sent); err != nil {
		return nil, []errorenvelope.Failure{invalid("models", "models must be a list of models: "+err.Error())}
	}
	if len(sent) > maxPickedModels {
		return nil, []errorenvelope.Failure{invalid("models", "Too many models in one request")}
	}
	picked := make([]pickedModel, 0, len(sent))
	seen := make(map[string]bool, len(sent))
	for _, model := range sent {
		model.ModelID = strings.TrimSpace(model.ModelID)
		switch {
		case model.ModelID == "" || utf8.RuneCountInString(model.ModelID) > maxModelIDChars:
			return nil, []errorenvelope.Failure{invalid("models", "Every model needs a model_id of at most 255 characters")}
		case model.DisplayName != nil && utf8.RuneCountInString(*model.DisplayName) > maxModelIDChars:
			return nil, []errorenvelope.Failure{invalid("models", "A model's display_name can be at most 255 characters")}
		case model.MaxInputTokens != nil && *model.MaxInputTokens <= 0:
			return nil, []errorenvelope.Failure{invalid("models", "A model's max_input_tokens must be positive")}
		}
		switch model.Source {
		case "":
			model.Source = "DISCOVERED"
		case "DISCOVERED", "MANUAL":
		default:
			return nil, []errorenvelope.Failure{invalid("models", `A model's source must be "DISCOVERED" or "MANUAL"`)}
		}
		var catalogue map[string]json.RawMessage
		if len(model.Metadata) > 0 && json.Unmarshal(model.Metadata, &catalogue) != nil {
			return nil, []errorenvelope.Failure{invalid("models", "A model's metadata must be an object")}
		}
		model.Catalog = model.Metadata
		if catalogue == nil {
			model.Catalog = json.RawMessage(`{}`)
		}
		if seen[model.ModelID] {
			continue
		}
		seen[model.ModelID] = true
		picked = append(picked, model.pickedModel)
	}
	return picked, nil
}

// paramsBag is the `litellm_params` object of an `extra`, or nil.
func paramsBag(extra []byte) json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal(extra, &object) != nil {
		return nil
	}
	bag := object["litellm_params"]
	var fields map[string]json.RawMessage
	if json.Unmarshal(bag, &fields) != nil || fields == nil {
		return nil
	}
	return bag
}
