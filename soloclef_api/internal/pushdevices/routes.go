// Package pushdevices is the phone's side of push notifications: which
// phones take this account's pushes, and which kinds of push it wants.
//
//	POST   /api/v1/push/devices       register this phone, bound to its sign-in
//	DELETE /api/v1/push/devices       forget this phone ({token} body, or ?token=)
//	GET    /api/v1/push/preferences   every kind with its choice
//	PUT    /api/v1/push/preferences   change some choices
//
// # No sender yet
//
// The Go stack has no push sender: nothing in this tier or the worker sends
// to Expo's push service yet. These routes keep the phone app's
// registration and its Notifications screen working, and keep the rows a
// sender will read (push_devices, push_preferences) correct and owned, so a
// sender can be added without touching the app. Until then no push arrives.
//
// # The rules
//
// Session only: a token names a phone that shows this person's account, so
// an API key or an agent may not register one or change what it is sent.
// A phone registers with the sign-in it belongs to (`session_id`, the
// family its sign-in answered) and only while that sign-in is the account's
// and live; signing that device out removes the row (internal/signin), so a
// signed-out phone is not woken by the account's notifications. A
// registration naming a sign-in that is not live is refused with 401 and
// PUSH_SESSION_ENDED, and the app takes its signed-out path.
//
// What is better than the Python: the kinds offered are only the events the
// product still has (mentions, invites and credits went with comments,
// workspaces and billing), and a write that cannot be read is refused with
// a 422 that says which field, instead of a pydantic dump.
package pushdevices

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// The paths.
const (
	DevicesPath     = "/api/v1/push/devices"
	PreferencesPath = "/api/v1/push/preferences"
)

// Served is every route of the package, all behind the gate.
func Served() []string {
	return []string{
		http.MethodPost + " " + DevicesPath,
		http.MethodDelete + " " + DevicesPath,
		http.MethodGet + " " + PreferencesPath,
		http.MethodPut + " " + PreferencesPath,
	}
}

// The limits of what a registration carries: the columns' widths. A token
// is Expo's, "ExponentPushToken[…]", far shorter than its column.
const (
	tokenMax    = 255
	labelMax    = 120
	languageMax = 16
	// bodyMax bounds every body these routes read: a token, a name, a
	// language and a handful of switches.
	bodyMax = 8 << 10
)

// sessionEnded is the code of a registration whose sign-in is not live.
const sessionEnded = "PUSH_SESSION_ENDED"

// Kind is one kind of push, a switch on the app's Notifications screen.
// The app draws exactly the list it is sent and carries none of its own, so
// a kind added here shows up on phones without an app update.
type Kind struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Kinds is every kind, in the order the screen lists them. Each is on until
// the person turns it off.
var Kinds = []Kind{
	{"answer", "Chat answers", "An answer that finished, or failed, while you were away from the chat."},
	{"approval", "Approvals", "A chat is waiting for you to approve a step."},
	{"output", "Studio outputs", "An audio overview, report, slides or other output is ready or failed."},
	{"automation", "Automations", "A run finished or failed."},
	{"source", "Sources", "A source finished indexing, or could not be added."},
	{"announcement", "Announcements", "From the site's administrators."},
}

// Routes serves the four routes.
type Routes struct {
	Database DBTX
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

func (r Routes) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

// Register mounts the four routes.
func (r Routes) Register(api huma.API) {
	mount := func(id, method, path, summary string, serve func(huma.Context, string)) {
		huma.Register(api, huma.Operation{
			OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"push"},
		}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: func(ctx huma.Context) {
				if userID, ok := r.sessionCaller(ctx); ok {
					serve(ctx, userID)
				}
			}}, nil
		})
	}
	mount("register-push-device", http.MethodPost, DevicesPath, "This phone takes this account's pushes", r.register)
	mount("forget-push-device", http.MethodDelete, DevicesPath, "This phone stops taking this account's pushes", r.forget)
	mount("read-push-preferences", http.MethodGet, PreferencesPath, "Every kind of push with its choice", r.readPreferences)
	mount("save-push-preferences", http.MethodPut, PreferencesPath, "Change which kinds of push are sent", r.savePreferences)
}

// sessionCaller is the caller, or false with the refusal written.
func (r Routes) sessionCaller(ctx huma.Context) (string, bool) {
	response := humawriter.New(ctx)
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		r.fail(ctx, "a push route ran without an authenticated caller", nil)
		return "", false
	}
	if !caller.IsSession() {
		r.Errors.Write(response, http.StatusForbidden, "This needs a signed-in session, not an API key.", requestcontext.ID(ctx.Context()))
		return "", false
	}
	return caller.UserID, true
}

func (r Routes) fail(ctx huma.Context, reason string, cause error) {
	requestID := requestcontext.ID(ctx.Context())
	if !errors.Is(cause, context.Canceled) {
		r.Logger.ErrorContext(ctx.Context(), "a push route could not be served",
			slog.String("request_id", requestID), slog.String("reason", reason), slog.Any("error", cause))
	}
	r.Errors.Write(humawriter.New(ctx), http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}

func (r Routes) invalid(ctx huma.Context, field, message string) {
	r.Errors.WriteValidation(humawriter.New(ctx), requestcontext.ID(ctx.Context()),
		[]errorenvelope.Failure{{Location: []string{"body", field}, Message: message}})
}

// readBody is the request's body, at most bodyMax bytes; false when longer.
func readBody(ctx huma.Context) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(ctx.BodyReader(), bodyMax+1))
	return body, err == nil && len(body) <= bodyMax
}

// expoToken is Expo's push token: "ExponentPushToken[…]" or
// "ExpoPushToken[…]", no whitespace. Anything else is not a phone's.
var expoToken = regexp.MustCompile(`^(ExponentPushToken|ExpoPushToken)\[[^\s\]]+\]$`)

func isExpoToken(value string) bool {
	return len(value) <= tokenMax && expoToken.MatchString(value)
}

// sessionShape is a sign-in's family id: a UUID in canonical text.
var sessionShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// collapsed is the text with its whitespace collapsed, at most max
// characters, or nil when nothing is left.
func collapsed(value *string, max int) *string {
	if value == nil {
		return nil
	}
	text := strings.Join(strings.Fields(*value), " ")
	if utf8.RuneCountInString(text) > max {
		text = string([]rune(text)[:max])
	}
	if text == "" {
		return nil
	}
	return &text
}

type deviceOut struct {
	ID         int64     `json:"id"`
	Token      string    `json:"token"`
	Platform   string    `json:"platform"`
	Label      *string   `json:"label"`
	Language   *string   `json:"language"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

// register binds this phone's token to the account, under the sign-in it
// names.
func (r Routes) register(ctx huma.Context, userID string) {
	body, ok := readBody(ctx)
	if !ok {
		r.Errors.Write(humawriter.New(ctx), http.StatusRequestEntityTooLarge, "The registration is too large.", requestcontext.ID(ctx.Context()))
		return
	}
	var asked struct {
		Token     string  `json:"token"`
		Platform  string  `json:"platform"`
		Label     *string `json:"label"`
		Language  *string `json:"language"`
		SessionID string  `json:"session_id"`
	}
	if err := json.Unmarshal(body, &asked); err != nil {
		r.invalid(ctx, "token", "The body is not a push registration.")
		return
	}
	switch {
	case !isExpoToken(asked.Token):
		r.invalid(ctx, "token", "Not an Expo push token.")
		return
	case asked.Platform != "ios" && asked.Platform != "android":
		r.invalid(ctx, "platform", "Input should be 'ios' or 'android'")
		return
	case !sessionShape.MatchString(asked.SessionID):
		r.invalid(ctx, "session_id", "Input should be a valid UUID")
		return
	}
	now := r.now()
	q := New(r.Database)
	family := strings.ToLower(asked.SessionID)
	live, err := q.FamilyIsLive(ctx.Context(), FamilyIsLiveParams{UserID: userID, FamilyID: family, Now: now})
	if err != nil {
		r.fail(ctx, "could not read the sign-in", err)
		return
	}
	if !live {
		r.Errors.WriteCoded(humawriter.New(ctx), http.StatusUnauthorized, sessionEnded,
			"This sign-in has ended; sign in again", requestcontext.ID(ctx.Context()))
		return
	}
	language := asked.Language
	if language != nil {
		trimmed := strings.TrimSpace(*language)
		language = &trimmed
	}
	device, err := q.UpsertDevice(ctx.Context(), UpsertDeviceParams{
		UserID: userID, Token: asked.Token, Platform: asked.Platform,
		Label: collapsed(asked.Label, labelMax), Language: collapsed(language, languageMax),
		FamilyID: family, Now: now,
	})
	if err != nil {
		r.fail(ctx, "could not register the phone", err)
		return
	}
	send(ctx, deviceOut{
		ID: device.ID, Token: device.Token, Platform: device.Platform,
		Label: device.Label, Language: device.Language, LastSeenAt: device.LastSeenAt.UTC(),
	})
}

// forget stops this phone taking the account's pushes. The token comes in
// the JSON body, which proxies do not log; the app's first builds sent it as
// `?token=`, which is still read when there is no body. It is the same 204
// whether or not the token was registered to this account, because a phone
// signing out has nothing to do with the difference, and another account's
// registration is never touched.
func (r Routes) forget(ctx huma.Context, userID string) {
	body, ok := readBody(ctx)
	if !ok {
		r.Errors.Write(humawriter.New(ctx), http.StatusRequestEntityTooLarge, "The request is too large.", requestcontext.ID(ctx.Context()))
		return
	}
	token := ""
	if len(strings.TrimSpace(string(body))) > 0 {
		var removal struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(body, &removal); err != nil {
			r.invalid(ctx, "token", "The body is not a device removal.")
			return
		}
		token = removal.Token
	} else {
		token = ctx.Query("token")
	}
	token = strings.TrimSpace(token)
	if isExpoToken(token) {
		if err := New(r.Database).RemoveDevice(ctx.Context(), RemoveDeviceParams{UserID: userID, Token: token}); err != nil {
			r.fail(ctx, "could not forget the phone", err)
			return
		}
	}
	ctx.SetHeader("Cache-Control", "no-store")
	ctx.SetStatus(http.StatusNoContent)
}

type preferencesOut struct {
	Choices map[string]bool `json:"choices"`
	Kinds   []Kind          `json:"kinds"`
}

// merged is the saved choices over the defaults (every kind on). A saved
// key that is no longer a kind, or a value that is not a switch, is left
// out, so the answer always has exactly one switch per kind.
func merged(saved []byte) map[string]bool {
	choices := make(map[string]bool, len(Kinds))
	for _, kind := range Kinds {
		choices[kind.Kind] = true
	}
	var stored map[string]any
	if len(saved) > 0 && json.Unmarshal(saved, &stored) == nil {
		for key, value := range stored {
			if on, isSwitch := value.(bool); isSwitch {
				if _, known := choices[key]; known {
					choices[key] = on
				}
			}
		}
	}
	return choices
}

func (r Routes) readPreferences(ctx huma.Context, userID string) {
	saved, err := New(r.Database).Choices(ctx.Context(), userID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		r.fail(ctx, "could not read the push choices", err)
		return
	}
	send(ctx, preferencesOut{Choices: merged(saved), Kinds: Kinds})
}

// savePreferences changes the kinds named and keeps the rest. A kind this
// tier does not know is refused, not stored: it would never be read.
func (r Routes) savePreferences(ctx huma.Context, userID string) {
	body, ok := readBody(ctx)
	if !ok {
		r.Errors.Write(humawriter.New(ctx), http.StatusRequestEntityTooLarge, "The choices are too large.", requestcontext.ID(ctx.Context()))
		return
	}
	var asked struct {
		Choices map[string]bool `json:"choices"`
	}
	if err := json.Unmarshal(body, &asked); err != nil || asked.Choices == nil {
		r.invalid(ctx, "choices", "Input should be an object of switches, one per kind")
		return
	}
	known := merged(nil)
	for key := range asked.Choices {
		if _, ok := known[key]; !ok {
			r.invalid(ctx, "choices", "Not a kind of push: "+key)
			return
		}
	}
	changes, err := json.Marshal(asked.Choices)
	if err != nil {
		r.fail(ctx, "could not write the choices", err)
		return
	}
	saved, err := New(r.Database).SaveChoices(ctx.Context(), SaveChoicesParams{UserID: userID, Choices: changes, Now: r.now()})
	if err != nil {
		r.fail(ctx, "could not save the push choices", err)
		return
	}
	send(ctx, preferencesOut{Choices: merged(saved), Kinds: Kinds})
}

// send writes a JSON answer nobody caches: it is one account's.
func send(ctx huma.Context, value any) {
	response := humawriter.New(ctx)
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(value)
}
