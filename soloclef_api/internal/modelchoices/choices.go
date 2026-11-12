package modelchoices

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// Lists is the proxy's own lists of what it offers; chat.Catalogue is the
// one a server runs with, so the pickers here and the chat's model menu are
// drawn from one read of the proxy.
type Lists interface {
	// Names is the chat models, sorted.
	Names(ctx context.Context) ([]string, error)
	// ImageNames is the models that draw, sorted.
	ImageNames(ctx context.Context) ([]string, error)
}

// Stored is where an account's choices are kept.
type Stored interface {
	// Of is every choice the account has made, by purpose.
	Of(ctx context.Context, userID string) (map[Purpose]string, error)
	// Choose stores one choice and Clear removes one.
	Choose(ctx context.Context, userID string, purpose Purpose, name string) error
	Clear(ctx context.Context, userID string, purpose Purpose) error
}

// OwnMenu is the account's own OpenAI-compatible connections' models
// (ownmodels.Connections), which every picker offers beside the proxy's.
type OwnMenu interface {
	Menu(ctx context.Context, userID string) ([]ownmodels.Option, error)
}

// Rows is Stored in Postgres: the account's rows of `user_models`.
type Rows struct {
	Pool *pgxpool.Pool
}

func (r Rows) Of(ctx context.Context, userID string) (map[Purpose]string, error) {
	rows, err := New(r.Pool).ChoicesOfAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	chosen := make(map[Purpose]string, len(rows))
	for _, row := range rows {
		chosen[Purpose(row.Purpose)] = row.Model
	}
	return chosen, nil
}

func (r Rows) Choose(ctx context.Context, userID string, purpose Purpose, name string) error {
	return New(r.Pool).ChooseModel(ctx, ChooseModelParams{UserID: userID, Purpose: string(purpose), Model: name})
}

func (r Rows) Clear(ctx context.Context, userID string, purpose Purpose) error {
	return New(r.Pool).ClearModel(ctx, ClearModelParams{UserID: userID, Purpose: string(purpose)})
}

// ErrProxyUnreachable is a list the proxy could not be asked for. It is an
// error and never an empty list: an empty list would say every stored
// choice had been withdrawn on the day the proxy was merely down.
var ErrProxyUnreachable = errors.New("modelchoices: the model proxy's list could not be read")

// ErrNotOffered is a job whose model the proxy does not list, or that has
// no model at all. The job is not done by some other model instead.
var ErrNotOffered = errors.New("modelchoices: the model for this job is not offered")

// Choices decides which model does each job for an account.
type Choices struct {
	Lists  Lists
	Stored Stored
	// ChatDefault is the deployment's chat model (CHAT_MODEL): what
	// answers an account that chose none, and so the default of quick and
	// studio for that account.
	ChatDefault string
	// VisionDefault and ImageDefault are the deployment's two picture
	// models. Either may be empty: the deployment then does that job only
	// for an account that chose a model.
	VisionDefault string
	ImageDefault  string
	// Own is the account's own models, offered beside the proxy's for each
	// job they can do. Check requires it; it is nil only in a test about
	// the proxy's lists alone.
	Own OwnMenu
}

// Check reports what is missing. Called once at start-up.
func (c Choices) Check() error {
	switch {
	case c.Lists == nil:
		return errors.New("modelchoices: the proxy's lists are not set")
	case c.Stored == nil:
		return errors.New("modelchoices: the stored choices are not set")
	case c.ChatDefault == "":
		return errors.New("modelchoices: the default chat model is not set")
	case c.Own == nil:
		return errors.New("modelchoices: the account's own model connections are not set")
	}
	return nil
}

// Chosen is one job's model as a picker shows it.
type Chosen struct {
	// Selected is the account's own choice for the job, or "".
	Selected string
	// Default is what does the job for an account with no choice, or "".
	Default string
	// Model is what would do the job now: Selected, else Default. It is ""
	// only when there is neither.
	Model string
	// Offered is whether the menu offers Model for this kind of job.
	Offered bool
	// Models is everything the account may choose for the job: the proxy's
	// names, then the account's own models that do the job.
	Models []string
	// Own describes the account's own models in Models.
	Own []ownmodels.Option
}

// Look reads every job's model for an account against the proxy's lists:
// one read of the account's rows and one (remembered) read of the proxy,
// however many jobs are asked about.
func (c Choices) Look(ctx context.Context, userID string, purposes ...Purpose) (map[Purpose]Chosen, error) {
	stored, err := c.Stored.Of(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("modelchoices: reading the account's choices: %w", err)
	}
	chat, err := c.Lists.Names(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProxyUnreachable, err)
	}
	var own []ownmodels.Option
	if c.Own != nil {
		if own, err = c.Own.Menu(ctx, userID); err != nil {
			return nil, fmt.Errorf("modelchoices: reading the account's own models: %w", err)
		}
	}
	looked := make(map[Purpose]Chosen, len(purposes))
	for _, purpose := range purposes {
		chosen := Chosen{Selected: stored[purpose], Models: chat}
		switch purpose {
		case Chat:
			chosen.Default = c.ChatDefault
		case Quick, Studio:
			// The model answering the account's chat: its own choice first.
			chosen.Default = stored[Chat]
			if chosen.Default == "" {
				chosen.Default = c.ChatDefault
			}
		case Vision:
			chosen.Default = c.VisionDefault
		case Image:
			chosen.Default = c.ImageDefault
			if chosen.Models, err = c.Lists.ImageNames(ctx); err != nil {
				return nil, fmt.Errorf("%w: %w", ErrProxyUnreachable, err)
			}
		default:
			return nil, fmt.Errorf("modelchoices: %q is not a purpose", purpose)
		}
		chosen.Models, chosen.Own = withOwn(chosen.Models, own, purpose)
		chosen.Model = chosen.Selected
		if chosen.Model == "" {
			chosen.Model = chosen.Default
		}
		chosen.Offered = chosen.Model != "" && slices.Contains(chosen.Models, chosen.Model)
		looked[purpose] = chosen
	}
	return looked, nil
}

// withOwn is a job's menu: the proxy's names (less any spelt like an own
// choice, which would open the account's model instead), then the
// account's own models that do the job, by what each says it can do.
func withOwn(proxy []string, own []ownmodels.Option, purpose Purpose) ([]string, []ownmodels.Option) {
	names := make([]string, 0, len(proxy)+len(own))
	for _, name := range proxy {
		if !ownmodels.IsChoiceSpelling(name) {
			names = append(names, name)
		}
	}
	var fit []ownmodels.Option
	for _, option := range own {
		does := option.Chat
		switch purpose {
		case Vision:
			does = option.Vision
		case Image:
			does = option.Images
		}
		if does {
			fit = append(fit, option)
			names = append(names, option.ID)
		}
	}
	return names, fit
}

// ModelFor is the name of the model that does a job for an account, for
// the code that is about to do it. A model the proxy no longer lists is
// ErrNotOffered and no other model is tried.
func (c Choices) ModelFor(ctx context.Context, userID string, purpose Purpose) (string, error) {
	looked, err := c.Look(ctx, userID, purpose)
	if err != nil {
		return "", err
	}
	if chosen := looked[purpose]; chosen.Offered {
		return chosen.Model, nil
	}
	return "", ErrNotOffered
}
