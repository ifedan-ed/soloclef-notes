package ownmodels

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/openaicompat"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
)

// Rows is where an account's own connections are read: Store in Postgres.
type Rows interface {
	Menu(ctx context.Context, userID string) ([]Option, error)
	Endpoint(ctx context.Context, userID string, id int64) (Endpoint, bool, error)
}

// Connections is an account's own connections for the code that runs on
// its chosen model: the menu to choose from, and a client for a choice.
type Connections struct {
	Rows   Rows
	Keys   KeySeal
	Egress Egress
	// Retry is how a call that failed for a reason that may pass is tried
	// again: the proxy's own policy, so a person's endpoint is treated as
	// patiently as the deployment's models are.
	Retry model.RetryPolicy
}

// Check reports what is missing. Called once at start-up.
func (c Connections) Check() error {
	switch {
	case c.Rows == nil:
		return errors.New("ownmodels: the connections' rows are not set")
	case !c.Keys.Ready():
		return errors.New("ownmodels: the key seal is not set")
	case c.Retry.Attempts < 1 || c.Retry.Backoff <= 0 || c.Retry.MaxBackoff < c.Retry.Backoff:
		return errors.New("ownmodels: the retry policy is not set")
	}
	return c.Egress.Check()
}

// Menu is the account's own models a menu may offer.
func (c Connections) Menu(ctx context.Context, userID string) ([]Option, error) {
	return c.Rows.Menu(ctx, userID)
}

// Unavailable is a chosen own model that cannot be called, in a sentence
// the person can act on: it was removed or switched off, its key can no
// longer be opened, or its address is refused. No other model is tried.
type Unavailable struct {
	Message string
	// Cause is the failure behind it, for the log.
	Cause error
}

func (u *Unavailable) Error() string { return u.Message }
func (u *Unavailable) Unwrap() error { return u.Cause }

// reached is one model of the account's own connection, opened: what a
// call to it needs, with the key and the headers in the clear.
type reached struct {
	endpoint Endpoint
	base     string
	key      string
	headers  map[string]string
	client   *http.Client
}

// reach opens one model of the account's own connection: its row, its key
// and headers, its address checked and the guard's client for it. The chat
// (Open) and the vision models (OpenVision) are reached the same way here,
// so neither can skip a check the other makes.
func (c Connections) reach(ctx context.Context, userID string, id int64) (reached, error) {
	endpoint, found, err := c.Rows.Endpoint(ctx, userID, id)
	if err != nil {
		return reached{}, fmt.Errorf("ownmodels: reading the connection: %w", err)
	}
	if !found {
		return reached{}, &Unavailable{Message: "The model you chose from your own connection has been removed. Choose another model in the model menu."}
	}
	if !endpoint.Enabled {
		return reached{}, &Unavailable{Message: "The model you chose, " + endpoint.Label + ", is switched off in your model connections. Switch it on, or choose another model."}
	}
	key, err := c.Keys.Open(userID, endpoint.SealedKey)
	if err != nil {
		return reached{}, &Unavailable{Message: "The key of your connection for " + endpoint.Label + " can no longer be read. Enter the key again in your model connections.", Cause: err}
	}
	headers, err := OpenHeaders(c.Keys, userID, endpoint.SealedHeaders)
	if err != nil {
		return reached{}, &Unavailable{Message: "The headers of your connection for " + endpoint.Label + " can no longer be read. Enter them again in your model connections.", Cause: err}
	}
	base, err := CheckAddress(endpoint.BaseURL)
	if err != nil {
		return reached{}, &Unavailable{Message: RefusalSentence(err, endpoint.BaseURL), Cause: err}
	}
	client, err := c.Egress.Client(ctx, base)
	if err != nil {
		return reached{}, err
	}
	return reached{endpoint: endpoint, base: base, key: key, headers: headers, client: client}, nil
}

// Open is a client for one model of the account's own connection, called
// at its own address. It does not call the endpoint; the guard runs when
// the first request connects.
func (c Connections) Open(ctx context.Context, userID string, id int64) (model.Model, error) {
	opened, err := c.reach(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	client, err := openaicompat.New(openaicompat.Config{
		BaseURL: opened.base, APIKey: opened.key, Model: opened.endpoint.ModelID, HTTPClient: opened.client, Headers: opened.headers,
	})
	if err != nil {
		return nil, &Unavailable{Message: "Your connection for " + opened.endpoint.Label + " cannot be called as it is saved. Check it in your model connections.", Cause: err}
	}
	return model.WithRetry(guarded{client, opened.endpoint.BaseURL}, c.Retry)
}

// OpenVision is one model of the account's own connection as a vision
// model: the same address, key, headers and guard as Open, with the
// guard's refusal read as one (vision.Endpoint.Refused) so that it is not
// retried as a model that is down. timeout bounds one description.
//
// It is not wrapped in Retry: a description that fails for a reason that
// may pass is tried again by its caller (the indexer hands the document
// back; a chat tool says to try again), as one by the proxy's models is.
func (c Connections) OpenVision(ctx context.Context, userID string, id int64, timeout time.Duration) (vision.Endpoint, error) {
	opened, err := c.reach(ctx, userID, id)
	if err != nil {
		return vision.Endpoint{}, err
	}
	base := opened.endpoint.BaseURL
	return vision.Endpoint{
		BaseURL: opened.base, Key: opened.key, Model: opened.endpoint.ModelID, Headers: opened.headers,
		HTTP: opened.client, Timeout: timeout,
		Refused: func(err error) (string, bool) {
			if !IsRefusal(err) {
				return "", false
			}
			return RefusalSentence(err, base), true
		},
	}, nil
}

// guarded turns the guard's refusal, which surfaces as a failed request
// (model.Error's Cause), into a failure that is not retried: the address
// will resolve inside the network again. The sentence for it is the log's;
// the settings screen's test says it to the person.
type guarded struct {
	model.Model
	base string
}

func (g guarded) Generate(ctx context.Context, request model.Request, stream func(model.Chunk)) (model.Response, error) {
	response, err := g.Model.Generate(ctx, request, stream)
	if err != nil && IsRefusal(err) {
		return response, &model.Error{Message: RefusalSentence(err, g.base)}
	}
	return response, err
}

// Opener opens a model of an account's own connection: Connections.
type Opener interface {
	Open(ctx context.Context, userID string, id int64) (model.Model, error)
}

// Resolver is the one place a stored choice is read as either a proxy name
// or a model of the account's own connection (package comment, "Naming a
// choice"). Every caller that opens an account's chosen model opens it
// through Open.
type Resolver struct {
	// Proxy builds the client for a proxy name. It does not call the proxy.
	Proxy func(name string) (model.Model, error)
	// Own opens a model of the account's own connection.
	Own Opener
}

// Check reports what is missing. Called once at start-up.
func (r Resolver) Check() error {
	switch {
	case r.Proxy == nil:
		return errors.New("ownmodels: the resolver has no proxy opener")
	case r.Own == nil:
		return errors.New("ownmodels: the resolver has no opener for own connections")
	}
	return nil
}

// Open is the client for the model a choice names, for the account whose
// choice it is.
func (r Resolver) Open(ctx context.Context, userID, name string) (model.Model, error) {
	if id, own := ParseChoice(name); own {
		return r.Own.Open(ctx, userID, id)
	}
	return r.Proxy(name)
}

// VisionOpener opens a model of an account's own connection as a vision
// model: Connections.
type VisionOpener interface {
	OpenVision(ctx context.Context, userID string, id int64, timeout time.Duration) (vision.Endpoint, error)
}

// VisionResolver is Resolver for the vision models: a stored vision choice
// read as a proxy name, or as a model of the account's own connection,
// by the one rule (ParseChoice). Every caller that shows an account's
// vision model a picture opens it through Open (soloclef_assistant/
// accountvision decides which choice that is).
type VisionResolver struct {
	// Proxy is the model proxy's vision models.
	Proxy vision.Client
	// Own opens a model of the account's own connection.
	Own VisionOpener
}

// Check reports what is missing. Called once at start-up.
func (r VisionResolver) Check() error {
	if r.Own == nil {
		return errors.New("ownmodels: the vision resolver has no opener for own connections")
	}
	return r.Proxy.Check()
}

// Open is the vision model a choice names, for the account whose choice it
// is. A model of its own connection that cannot be called is an
// *Unavailable with the sentence for it.
func (r VisionResolver) Open(ctx context.Context, userID, name string) (vision.Endpoint, error) {
	if id, own := ParseChoice(name); own {
		return r.Own.OpenVision(ctx, userID, id, r.Proxy.Timeout)
	}
	return r.Proxy.Endpoint(name)
}

// ForDeployment is the Connections a server runs with: the rows in pool,
// keys opened with the deployment's secret (the one the API tier sealed
// them with), the operating system's resolver behind the guard and the
// operator's allow-list read from site_settings at every call. Each tier
// that runs an account's chosen model builds it the same way here, so the
// guard cannot be wired differently in one of them.
func ForDeployment(pool *pgxpool.Pool, secret string, connectTimeout time.Duration, retry model.RetryPolicy) (Connections, error) {
	keys, err := NewKeySeal(secret)
	if err != nil {
		return Connections{}, err
	}
	rows := Store{Pool: pool}
	connections := Connections{
		Rows: rows, Keys: keys, Retry: retry,
		Egress: Egress{Resolve: pagereader.SystemResolve, AllowedHosts: rows.AllowedHosts, ConnectTimeout: connectTimeout},
	}
	return connections, connections.Check()
}

// NoConnections is an account with no own connections, said explicitly:
// what a test about the proxy's models alone gives in place of
// Connections. Its menu is empty and nothing opens.
type NoConnections struct{}

// Menu is empty.
func (NoConnections) Menu(context.Context, string) ([]Option, error) { return nil, nil }

// Open opens nothing.
func (NoConnections) Open(context.Context, string, int64) (model.Model, error) {
	return nil, noConnection
}

// OpenVision opens nothing.
func (NoConnections) OpenVision(context.Context, string, int64, time.Duration) (vision.Endpoint, error) {
	return vision.Endpoint{}, noConnection
}

var noConnection = &Unavailable{Message: "There is no model of your own connection by that name. Choose another model in the model menu."}
