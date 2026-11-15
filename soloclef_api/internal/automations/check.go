package automations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"git.danvics.com/danvics/soloclef/soloclef_automations/definition"
)

// Checker checks a new automation exactly as saving one does: its shape
// (soloclef_automations/definition, which the worker runs it with) and
// what it names that only this tier can vouch for (vouch).
//
// It is a type of its own, not a method of Routes, because two callers need
// the same answer: the create route, and the chat's create_automation tool,
// which checks its draft before showing it (soloclef_assistant/
// automationdrafts). A draft the card shows is then one Create will accept,
// unless something it names changed in between, and the person is never
// shown a draft that a second copy of these rules would have passed.
type Checker struct {
	Pool   *pgxpool.Pool
	Models ModelMenu
	// SendsMail is Routes.SendsMail.
	SendsMail bool
}

// newAutomation is the body of POST /automations: the automation, its first
// triggers and, when it comes from a chat draft's card, the draft.
type newAutomation struct {
	Name        string          `json:"name"`
	Description *string         `json:"description"`
	Definition  json.RawMessage `json:"definition"`
	Triggers    []newTrigger    `json:"triggers"`
	// DraftID is the chat draft (automation_drafts) the card created this
	// from. Optional: the builder sends none.
	DraftID *string `json:"draft_id"`
}

// ready is a new automation that passed every check, in the form it is
// stored.
type ready struct {
	parsed   definition.Definition
	stored   []byte
	triggers []checked
}

// prepare checks a new automation. problems are for the person, in words;
// an error is a check that could not be made (errModelsUnreachable among
// them).
func (c Checker) prepare(ctx context.Context, userID string, request newAutomation) (ready, []string, error) {
	var problems []string
	if name := strings.TrimSpace(request.Name); name == "" || len([]rune(request.Name)) > definition.MaxNameChars {
		problems = append(problems, "name must be 1 to "+strconv.Itoa(definition.MaxNameChars)+" characters")
	}
	parsed, definitionProblems := definition.Parse(request.Definition)
	problems = append(problems, definitionProblems...)
	triggers := make([]checked, 0, len(request.Triggers))
	for i, trigger := range request.Triggers {
		ok, err := trigger.check()
		if err != nil {
			problems = append(problems, "triggers["+strconv.Itoa(i)+"]: "+err.Error())
			continue
		}
		triggers = append(triggers, ok)
	}
	if len(problems) > 0 {
		// What the definition names is asked about only once its shape is
		// right: a definition that cannot be read names nothing reliably.
		return ready{}, problems, nil
	}
	if problems, err := c.vouch(ctx, userID, parsed.Named()); err != nil || len(problems) > 0 {
		return ready{}, problems, err
	}
	stored, err := json.Marshal(parsed)
	if err != nil {
		return ready{}, nil, err
	}
	return ready{parsed: parsed, stored: stored, triggers: triggers}, nil, nil
}

// Check is what the chat's drafting tool asks before it shows a draft:
// everything wrong with request (a create request's JSON, without a
// draft_id), in words, or an error when the check could not be made.
func (c Checker) Check(ctx context.Context, userID string, request []byte) ([]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(request))
	decoder.DisallowUnknownFields()
	var parsed newAutomation
	if err := decoder.Decode(&parsed); err != nil {
		return []string{"the draft could not be read: " + err.Error()}, nil
	}
	if parsed.DraftID != nil {
		// A draft is made from a request; it cannot itself name one.
		return []string{"draft_id is not part of a draft"}, nil
	}
	_, problems, err := c.prepare(ctx, userID, parsed)
	if errors.Is(err, errModelsUnreachable) {
		// The tool tells the model the check could not be made; it is not
		// a problem with the draft for the model to fix.
		return nil, errors.New(assembly.ModelsUnreachable)
	}
	return problems, err
}

// errModelsUnreachable is a save that names a model while the model proxy
// cannot be asked whether it is offered.
var errModelsUnreachable = errors.New("automations: the model proxy could not be asked")

// vouch checks what a definition names that only this tier can: that each
// notebook and connected account is the caller's own, that each model is
// one the proxy offers, and that this server sends mail if the result is
// to be emailed. The problems are in words for the person saving.
//
// Another account's notebook and one that never existed read the same
// here: neither is "one of yours".
func (c Checker) vouch(ctx context.Context, userID string, named definition.Named) ([]string, error) {
	var problems []string
	q := New(c.Pool)
	absent := func(what string, wanted, owned []int64) {
		for _, id := range wanted {
			if !slices.Contains(owned, id) {
				problems = append(problems, what+" "+strconv.FormatInt(id, 10)+" is not one of yours")
			}
		}
	}
	if len(named.NotebookIDs) > 0 {
		owned, err := q.OwnedNotebooks(ctx, OwnedNotebooksParams{UserID: userID, Ids: named.NotebookIDs})
		if err != nil {
			return nil, err
		}
		absent("notebook", named.NotebookIDs, owned)
	}
	if len(named.ConnectorIDs) > 0 {
		owned, err := q.OwnedConnections(ctx, OwnedConnectionsParams{UserID: userID, Ids: named.ConnectorIDs})
		if err != nil {
			return nil, err
		}
		absent("connected account", named.ConnectorIDs, owned)
	}
	if named.Email && !c.SendsMail {
		problems = append(problems, "execution.email_result is not available: this server is not set up to send mail")
	}
	withdrawn, err := c.Models.NotOffered(ctx, userID, named.Models)
	var unanswerable *assembly.ModelError
	switch {
	case errors.As(err, &unanswerable):
		return nil, errModelsUnreachable
	case err != nil:
		return nil, err
	}
	for _, name := range withdrawn {
		problems = append(problems, "the model "+strconv.Quote(name)+" is not offered; choose one from the list")
	}
	return problems, nil
}
