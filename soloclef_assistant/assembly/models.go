package assembly

// Which model answers an account's turn: the account's own choice from the
// model proxy's list or from its own OpenAI-compatible connections
// (soloclef_assistant/ownmodels), else the deployment's default, and never
// a model the menu does not offer. The API tier's model menu (its chat-model routes)
// reads and writes the same choice; this is the half every tier that runs a
// turn needs.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// ModelsUnreachable is what a person is told when the proxy's list cannot
// be read.
const ModelsUnreachable = "The model service cannot be reached right now. Try again in a moment."

// ModelError is a turn that cannot be answered because of its model, in
// words for the person: the proxy's list could not be read (Unreachable),
// or the model that would answer is not on it. No other model is tried in
// either case.
type ModelError struct {
	// Unreachable says the proxy could not be asked; false means the model
	// is not offered.
	Unreachable bool
	Message     string
	// Cause is the failure behind an unreachable proxy, for the log.
	Cause error
}

func (e *ModelError) Error() string { return e.Message }
func (e *ModelError) Unwrap() error { return e.Cause }

// Catalogue is the proxy's list of chat model names, read from the proxy:
// the names its key may call, less the ones the proxy says are for
// something other than chat (read).
//
// The list is remembered for Fresh, per process: it is asked for at the
// start of every turn, and a round trip to the proxy before each one would
// be paid by every question to notice a change that happens a few times a
// year. A name removed from the proxy is therefore still accepted for up to
// Fresh by a process that listed it earlier; the proxy then refuses the
// call, and the turn ends with the same "no longer offered" sentence
// (finish in run.go). A list that cannot be read is an error and never an
// older list: stale is bounded by Fresh, not by how long the proxy is down.
type Catalogue struct {
	// URL is the proxy's OpenAI-compatible root, ending in /v1.
	URL string
	Key string
	// HTTP makes the request; Timeout bounds it.
	HTTP    *http.Client
	Timeout time.Duration
	// Fresh is how long a list is used before the proxy is asked again.
	Fresh time.Duration
	Now   func() time.Time
	// NotChat are names this deployment's own settings give another job
	// (its embedding model, its image model). What every other name is for
	// is the proxy's to say (forChat); these are left out whatever it says,
	// because a model the deployment embeds with is not a chat model even
	// on a proxy that states no purpose for it.
	NotChat []string

	mu sync.Mutex
	// names are the chat models and images the models that draw: one read
	// of the proxy gives both, so they are remembered and aged together.
	names  []string
	images []string
	readAt time.Time
}

// Check reports what is missing. Called once at start-up.
func (c *Catalogue) Check() error {
	switch {
	case strings.TrimSpace(c.URL) == "":
		return errors.New("chat: the model catalogue's URL is not set")
	case c.Key == "":
		return errors.New("chat: the model catalogue's key is not set")
	case c.HTTP == nil:
		return errors.New("chat: the model catalogue's HTTP client is not set")
	case c.Timeout <= 0 || c.Fresh <= 0:
		return fmt.Errorf("chat: the model catalogue's Timeout and Fresh must be positive, got %s and %s", c.Timeout, c.Fresh)
	case c.Now == nil:
		return errors.New("chat: the model catalogue's clock is not set")
	}
	return nil
}

// maxCatalogueBody bounds the list read from the proxy. A thousand models
// is about a hundred kilobytes.
const maxCatalogueBody = 4 << 20

// Names is the proxy's chat model names, sorted.
func (c *Catalogue) Names(ctx context.Context) ([]string, error) {
	names, _, err := c.lists(ctx)
	return names, err
}

// ImageNames is the proxy's names for the models that draw, sorted: the
// ones it says are for image generation. Unlike a chat model, a name the
// proxy states no purpose for is not offered here: a chat model sent a
// drawing request fails, so only what the proxy calls an image model is
// one (internal/modelchoices offers these for an account's image model).
func (c *Catalogue) ImageNames(ctx context.Context) ([]string, error) {
	_, images, err := c.lists(ctx)
	return images, err
}

// lists is both lists, from memory while they are fresh.
func (c *Catalogue) lists(ctx context.Context) ([]string, []string, error) {
	c.mu.Lock()
	if c.names != nil && c.Now().Sub(c.readAt) < c.Fresh {
		names, images := c.names, c.images
		c.mu.Unlock()
		return names, images, nil
	}
	c.mu.Unlock()

	// Read outside the lock: a slow proxy must not queue every turn of the
	// process behind one request. Two turns that both find the list old
	// both ask; the second answer replaces the first, and both are right.
	names, images, err := c.read(ctx)
	if err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	c.names, c.images, c.readAt = names, images, c.Now()
	c.mu.Unlock()
	return names, images, nil
}

// imageMode is the proxy's word for a model that draws (LiteLLM's
// model_info.mode).
const imageMode = "image_generation"

// chatModes are the proxy's words for a model that answers a conversation
// (LiteLLM's model_info.mode). Every other stated mode is a model for
// something else: embedding, image_generation, audio_speech,
// audio_transcription, rerank, moderation, completion and the rest.
var chatModes = map[string]bool{"chat": true, "responses": true}

// forChat decides whether a name is offered as a chat model from what the
// proxy says its deployments are for. The rule is one sentence: a name is
// left out when the proxy says it is for something else. A name the proxy
// states no purpose for is offered, because the proxy has not said it is
// anything but a chat model and most hand-configured chat models are
// exactly that; the cure for an image model that still shows is to give it
// its mode on the proxy, where the fact belongs.
func forChat(modes []string) bool {
	if len(modes) == 0 {
		return true
	}
	for _, mode := range modes {
		if chatModes[mode] {
			return true
		}
	}
	return false
}

// read asks the proxy two things at once: which names this key may call
// (GET /models, the OpenAI route) and what each is for (GET /model/info,
// LiteLLM's own). Both must answer. A proxy whose purposes cannot be read
// is an error like one whose list cannot: offering the unfiltered list
// instead would put image and speech models back in the picker on exactly
// the day something is already wrong.
func (c *Catalogue) read(ctx context.Context) (names, images []string, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	type stated struct {
		modes map[string][]string
		err   error
	}
	purposes := make(chan stated, 1)
	go func() {
		var info struct {
			Data []struct {
				Name string `json:"model_name"`
				Info struct {
					Mode *string `json:"mode"`
				} `json:"model_info"`
			} `json:"data"`
		}
		if err := c.get(ctx, "/model/info", "model purposes", &info); err != nil {
			purposes <- stated{err: err}
			return
		}
		// A name can have several deployments; each states its own mode.
		modes := map[string][]string{}
		for _, entry := range info.Data {
			if entry.Info.Mode != nil && *entry.Info.Mode != "" {
				modes[entry.Name] = append(modes[entry.Name], *entry.Info.Mode)
			}
		}
		purposes <- stated{modes: modes}
	}()

	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	listErr := c.get(ctx, "/models", "model list", &list)
	said := <-purposes
	if listErr != nil {
		return nil, nil, listErr
	}
	if said.err != nil {
		return nil, nil, said.err
	}
	// Empty lists, never nil: nil is how lists tells "not read yet".
	names, images = make([]string, 0, len(list.Data)), []string{}
	for _, entry := range list.Data {
		if entry.ID == "" {
			continue
		}
		modes := said.modes[entry.ID]
		if forChat(modes) && !slices.Contains(c.NotChat, entry.ID) && !slices.Contains(names, entry.ID) {
			names = append(names, entry.ID)
		}
		if slices.Contains(modes, imageMode) && !slices.Contains(images, entry.ID) {
			images = append(images, entry.ID)
		}
	}
	sort.Strings(names)
	sort.Strings(images)
	return names, images, nil
}

// get reads one JSON answer from the proxy. what names it in an error.
func (c *Catalogue) get(ctx context.Context, path, what string, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.URL, "/")+path, nil)
	if err != nil {
		return fmt.Errorf("chat: building the %s request: %w", what, err)
	}
	request.Header.Set("Authorization", "Bearer "+c.Key)
	request.Header.Set("Accept", "application/json")
	response, err := c.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("chat: asking the proxy for its %s: %w", what, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxCatalogueBody))
	if err != nil {
		return fmt.Errorf("chat: reading the proxy's %s: %w", what, err)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("chat: the proxy answered its %s with status %d", what, response.StatusCode)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("chat: the proxy's %s is not readable: %w", what, err)
	}
	return nil
}

// ModelLister is the proxy's list of chat model names; Catalogue is the one
// a server runs with.
type ModelLister interface {
	Names(ctx context.Context) ([]string, error)
}

// ModelChoice is where an account's choice of chat model is read.
type ModelChoice interface {
	// ChatModel is the account's chosen name, or "" if it has chosen none.
	ChatModel(ctx context.Context, userID string) (string, error)
}

// OwnModels is an account's own OpenAI-compatible connections:
// ownmodels.Connections is the one a server runs with.
type OwnModels interface {
	// Menu is the account's own models a menu may offer.
	Menu(ctx context.Context, userID string) ([]ownmodels.Option, error)
	// Open is a client for one of them, called at its own address.
	Open(ctx context.Context, userID string, id int64) (model.Model, error)
}

// Models decides which model answers a turn.
type Models struct {
	Catalogue ModelLister
	Choices   ModelChoice
	// Default is the proxy's name for the model an account that has chosen
	// none is answered by.
	Default string
	// Open returns the model behind a proxy name. It does not call the
	// proxy; it builds the client that will.
	Open func(name string) (model.Model, error)
	// Own is the account's own OpenAI-compatible connections, whose models
	// join the menu beside the proxy's and are called directly when chosen
	// (soloclef_assistant/ownmodels). Check requires it: a test about the
	// proxy's models alone says ownmodels.NoConnections. Left nil (a test
	// that never runs Check), there are no own models.
	Own OwnModels
}

// Check reports what is missing. Called once at start-up.
func (m *Models) Check() error {
	switch {
	case m.Catalogue == nil:
		return errors.New("chat: the model catalogue is not set")
	case m.Choices == nil:
		return errors.New("chat: the model choices are not set")
	case m.Default == "":
		return errors.New("chat: the default chat model is not set")
	case m.Open == nil:
		return errors.New("chat: the model opener is not set")
	case m.Own == nil:
		return errors.New("chat: the account's own model connections are not set")
	}
	return nil
}

// Chosen is an account's model as the picker shows it.
type Chosen struct {
	// Selected is the account's own choice, or "".
	Selected string
	// Name is the model that would answer now: the choice, else Default.
	Name string
	// Offered is whether the menu offers Name: the proxy lists it, or it
	// is one of the account's own models.
	Offered bool
	// Models is the menu: the proxy's list, then the account's own models
	// by their stored names (ownmodels.Choice).
	Models []string
	// Own describes the account's own models in Models, for the menu to
	// label them.
	Own []ownmodels.Option
}

// menu is the names an account may choose from: the proxy's chat models,
// then the account's own. A proxy name spelt like an own choice is left
// out: offered, it would open the account's model instead of the proxy's.
func (m *Models) menu(ctx context.Context, userID string) ([]string, []ownmodels.Option, error) {
	names, err := m.Catalogue.Names(ctx)
	if err != nil {
		return nil, nil, &ModelError{Unreachable: true, Message: ModelsUnreachable, Cause: err}
	}
	offered := make([]string, 0, len(names))
	for _, name := range names {
		if !ownmodels.IsChoiceSpelling(name) {
			offered = append(offered, name)
		}
	}
	if m.Own == nil {
		return offered, nil, nil
	}
	options, err := m.Own.Menu(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("chat: reading the account's own models: %w", err)
	}
	own := make([]ownmodels.Option, 0, len(options))
	for _, option := range options {
		if option.Chat {
			own = append(own, option)
			offered = append(offered, option.ID)
		}
	}
	return offered, own, nil
}

// OpenFor is the client for the model a stored name names, for the account
// whose choice it is: a model of its own connection, called at its own
// address, or a proxy name (ownmodels.Resolver, the one rule). An own model
// that cannot be called is a *ModelError with the sentence for it.
func (m *Models) OpenFor(ctx context.Context, userID, name string) (model.Model, error) {
	var own ownmodels.Opener = ownmodels.NoConnections{}
	if m.Own != nil {
		own = m.Own
	}
	opened, err := ownmodels.Resolver{Proxy: m.Open, Own: own}.Open(ctx, userID, name)
	var unavailable *ownmodels.Unavailable
	if errors.As(err, &unavailable) {
		return nil, &ModelError{Message: unavailable.Message, Cause: unavailable.Cause}
	}
	return opened, err
}

// labelOf is how a sentence names a model: an own model by its label.
func labelOf(name string, own []ownmodels.Option) string {
	for _, option := range own {
		if option.ID == name {
			return option.Label
		}
	}
	if ownmodels.IsChoiceSpelling(name) {
		return "a model of your own connection"
	}
	return name
}

// Look reads an account's choice against the proxy's list.
func (m *Models) Look(ctx context.Context, userID string) (Chosen, error) {
	selected, err := m.Choices.ChatModel(ctx, userID)
	if err != nil {
		return Chosen{}, fmt.Errorf("chat: reading the account's chat model: %w", err)
	}
	names, own, err := m.menu(ctx, userID)
	if err != nil {
		return Chosen{}, err
	}
	chosen := Chosen{Selected: selected, Name: selected, Models: names, Own: own}
	if selected == "" {
		chosen.Name = m.Default
	}
	chosen.Offered = slices.Contains(names, chosen.Name)
	return chosen, nil
}

// For returns the model that answers an account's turn, and its name. A
// name the proxy does not list is a *ModelError that says which name and
// whose choice it was.
func (m *Models) For(ctx context.Context, userID string) (model.Model, string, error) {
	chosen, err := m.Look(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	if !chosen.Offered {
		return nil, "", &ModelError{Message: modelGone(labelOf(chosen.Name, chosen.Own), chosen.Selected != "")}
	}
	opened, err := m.OpenFor(ctx, userID, chosen.Name)
	if err != nil {
		return nil, "", err
	}
	return opened, chosen.Name, nil
}

// NotOffered is which of the names the account's menu does not offer (the
// proxy's list and the account's own models), in the order given: what a
// tier that saves a model's name asks before saving it. A proxy that cannot
// be asked is a *ModelError saying so, never "all of them are fine".
func (m *Models) NotOffered(ctx context.Context, userID string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	listed, _, err := m.menu(ctx, userID)
	if err != nil {
		return nil, err
	}
	var withdrawn []string
	for _, name := range names {
		if !slices.Contains(listed, name) {
			withdrawn = append(withdrawn, name)
		}
	}
	return withdrawn, nil
}

// Named returns the model with this name, for a turn that names its own
// (an automation's step) rather than taking the account's choice. It is
// held to the account's menu exactly as a choice is: a name the menu does
// not offer is a *ModelError, and no other model answers instead.
func (m *Models) Named(ctx context.Context, userID, name string) (model.Model, string, error) {
	withdrawn, err := m.NotOffered(ctx, userID, []string{name})
	if err != nil {
		return nil, "", err
	}
	if len(withdrawn) > 0 {
		shown := name
		if ownmodels.IsChoiceSpelling(name) {
			shown = "a model of your own connection"
		}
		return nil, "", &ModelError{Message: "The model this automation is set to run on, " + shown + ", is no longer offered. Edit the automation and choose another model."}
	}
	opened, err := m.OpenFor(ctx, userID, name)
	if err != nil {
		return nil, "", err
	}
	return opened, name, nil
}

// modelGone is the sentence for a model the proxy does not offer. Whose
// choice it was decides who can fix it: an account's own choice is fixed
// in its model menu, the server's default by whoever runs the server.
func modelGone(name string, chosenByAccount bool) string {
	if chosenByAccount {
		return "The model you chose, " + name + ", is no longer offered. Choose another model in the model menu and send your message again."
	}
	return "You have not chosen a model, and the server's default model, " + name + ", is not offered. Choose a model in the model menu and send your message again."
}
