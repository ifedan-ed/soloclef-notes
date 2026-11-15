// Package pushdevices is the phone's side of push notifications: which
// phones take this account's pushes, and which kinds of push it wants.
//
//	POST   /api/v1/push/devices       register this phone, bound to its sign-in
//	DELETE /api/v1/push/devices       forget this phone ({token} body, or ?token=)
//	GET    /api/v1/push/preferences   every kind with its choice, and the quiet hours
//	PUT    /api/v1/push/preferences   change some choices, or the quiet hours
//
// # Who reads these rows
//
// The worker's push delivery (soloclef_notify/delivery) reads both: the
// phones to push to, and whether this person wants this kind pushed now.
// The kinds and the quiet-hours rules are soloclef_notify/pushpolicy's, so
// what is saved here and what is obeyed there are one definition.
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
// # Tokens
//
// A registration names its token's kind: "apns", an iPhone's own device
// token in hex, which the worker pushes straight through Apple, with the
// APNs environment it was minted in ("production" for App Store and
// TestFlight builds, "sandbox" for development builds); or "expo", an Expo
// push token, which the worker pushes through Expo's service (Android). Each
// is checked for its own shape. A registration with no kind is from an app
// build older than APNs, whose only token is Expo's, and is read as "expo";
// its token is still checked as Expo's, so nothing else is let in by it.
// A sign-in holds one token: registering a new one (an iPhone moving from
// Expo to APNs, a token its service replaced) removes the last, so the
// phone is never pushed twice.
//
// # The app's version
//
// A registration from an app build that knows it says which version of the
// app it runs (`app_version`, "1.2.0"); the worker's daily App Store check
// compares it with the store's and pushes "Soloclef 1.3.0 is out" to an
// iPhone on an older one (soloclef_worker/internal/appupdate). It is kept as
// sent on every registration, so a phone that updated and started again
// reads as current. A build from before this field sends none and is stored
// as none: such a build draws its own update banner, and nothing is guessed
// about a version it did not say. A version that is not a plain dotted
// number is refused with a 422 naming the field, never stored for the
// comparison to trip on (soloclef_notify/appversion holds the one rule).
//
// # The bundle
//
// An over-the-air update changes the JavaScript a build runs without a new
// version, so a registration also says which update the phone runs
// (`update_id`, expo-updates' UUID; null for the bundle built into the app)
// and for which native runtime (`runtime_version`). The server keeps
// answering every bundle still installed (internal/installedbuilds); these
// are how an operator sees that no phone runs an old one any more
// (`soloclef-api app-versions`). Both are kept as sent, and a value of the
// wrong shape is a 422 naming the field.
//
// What is better than the Python: the kinds offered are only the events
// something sends (mentions, invites and credits went with comments,
// workspaces and billing; the chat's answer and approval pushes were never
// sent), quiet hours are in the person's own time zone, and a write that
// cannot be read is refused with a 422 that says which field, instead of a
// pydantic dump.
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

	"git.danvics.com/danvics/soloclef/soloclef_notify/appversion"
	"git.danvics.com/danvics/soloclef/soloclef_notify/pushpolicy"
	"git.danvics.com/danvics/soloclef/soloclef_notify/pushsend"

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
// is Expo's, "ExponentPushToken[…]", or Apple's in hex (64 characters
// today), both far shorter than its column.
const (
	tokenMax    = 255
	labelMax    = 120
	languageMax = 16
	// bodyMax bounds every body these routes read: a token, a name, a
	// language, a handful of switches and a span of the day.
	bodyMax = 8 << 10
)

// sessionEnded is the code of a registration whose sign-in is not live.
const sessionEnded = "PUSH_SESSION_ENDED"

// Kinds is every kind of push, in the order the screens list them. The app
// and the web draw exactly the list they are sent and carry none of their
// own, so a kind added in soloclef_notify/pushpolicy shows up everywhere
// without a client release.
var Kinds = pushpolicy.Kinds

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

// apnsToken is Apple's device token as the phone app hands it over: its
// bytes in lower-case hex. Apple's are 32 bytes today and Apple says they
// may grow, so any even length from 32 to 100 bytes is taken; the worker's
// APNs sender checks the same shape before it sends.
var apnsToken = regexp.MustCompile(`^(?:[0-9a-f]{2}){32,100}$`)

func isAPNsToken(value string) bool {
	return apnsToken.MatchString(value)
}

// The APNs environments, as push_devices.apns_environment holds them
// (soloclef_notify/apns spells the same).
const (
	apnsProduction = "production"
	apnsSandbox    = "sandbox"
)

// pushToken is a registration's token once checked: its kind, the token as
// stored, and for an APNs token its environment.
type pushToken struct {
	kind        string
	token       string
	environment *string
}

// checkToken checks a registration's token against its kind, or answers
// the field and the sentence a refusal names.
func checkToken(kind *string, token, platform string, environment *string) (pushToken, string, string) {
	named := ""
	if environment != nil {
		named = *environment
	}
	switch {
	case kind == nil || *kind == pushsend.KindExpo:
		// No kind: an app build from before APNs, whose token is always
		// Expo's (the package's # Tokens).
		if !isExpoToken(token) {
			return pushToken{}, "token", "Not an Expo push token."
		}
		if named != "" {
			return pushToken{}, "environment", "An Expo token has no APNs environment."
		}
		return pushToken{kind: pushsend.KindExpo, token: token}, "", ""
	case *kind == pushsend.KindAPNs:
		// Hex has no case; one spelling keeps a phone's token one row.
		lower := strings.ToLower(token)
		switch {
		case !isAPNsToken(lower):
			return pushToken{}, "token", "Not an APNs device token (hex)."
		case platform != "ios":
			return pushToken{}, "platform", "An APNs token is an iPhone's: the platform is 'ios'."
		case named != apnsProduction && named != apnsSandbox:
			return pushToken{}, "environment", "Input should be 'production' or 'sandbox'"
		}
		return pushToken{kind: pushsend.KindAPNs, token: lower, environment: &named}, "", ""
	}
	return pushToken{}, "kind", "Input should be 'apns' or 'expo'"
}

// sessionShape is a UUID in canonical text: a sign-in's family id, and an
// update's id.
var sessionShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// runtimeShape is a runtime version as expo-updates writes one (a
// fingerprint's hex, or a version), and as push_devices' check admits.
var runtimeShape = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

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
	ID         int64   `json:"id"`
	Token      string  `json:"token"`
	Kind       string  `json:"kind"`
	Platform   string  `json:"platform"`
	Label      *string `json:"label"`
	Language   *string `json:"language"`
	AppVersion *string `json:"app_version"`
	// UpdateID and RuntimeVersion say which bundle the phone runs
	// (# The bundle).
	UpdateID       *string   `json:"update_id"`
	RuntimeVersion *string   `json:"runtime_version"`
	LastSeenAt     time.Time `json:"last_seen_at"`
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
		Token string `json:"token"`
		// Kind is "apns" or "expo"; absent from an app build before APNs.
		Kind *string `json:"kind"`
		// Environment is an APNs token's: "production" or "sandbox".
		Environment *string `json:"environment"`
		Platform    string  `json:"platform"`
		Label       *string `json:"label"`
		Language    *string `json:"language"`
		// AppVersion is the app's own version ("1.2.0"); absent from a
		// build before it was sent (# The app's version).
		AppVersion *string `json:"app_version"`
		// UpdateID is expo-updates' id of the update the phone runs (a
		// UUID), null for the bundle built into the app; RuntimeVersion
		// is the native runtime it was made for. Both absent from a
		// bundle before they were sent (# The bundle).
		UpdateID       *string `json:"update_id"`
		RuntimeVersion *string `json:"runtime_version"`
		SessionID      string  `json:"session_id"`
	}
	if err := json.Unmarshal(body, &asked); err != nil {
		r.invalid(ctx, "token", "The body is not a push registration.")
		return
	}
	token, field, refusal := checkToken(asked.Kind, asked.Token, asked.Platform, asked.Environment)
	switch {
	case field != "":
		r.invalid(ctx, field, refusal)
		return
	case asked.Platform != "ios" && asked.Platform != "android":
		r.invalid(ctx, "platform", "Input should be 'ios' or 'android'")
		return
	case !sessionShape.MatchString(asked.SessionID):
		r.invalid(ctx, "session_id", "Input should be a valid UUID")
		return
	}
	if asked.AppVersion != nil {
		if _, err := appversion.Parse(*asked.AppVersion); err != nil {
			r.invalid(ctx, "app_version", err.Error())
			return
		}
	}
	if asked.UpdateID != nil && !sessionShape.MatchString(*asked.UpdateID) {
		r.invalid(ctx, "update_id", "Input should be a valid UUID")
		return
	}
	if asked.RuntimeVersion != nil && !runtimeShape.MatchString(*asked.RuntimeVersion) {
		r.invalid(ctx, "runtime_version", "Input should be up to 64 letters, digits, dots, dashes or underscores")
		return
	}
	var updateID *string
	if asked.UpdateID != nil {
		lowered := strings.ToLower(*asked.UpdateID)
		updateID = &lowered
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
		UserID: userID, Token: token.token, Kind: token.kind, ApnsEnvironment: token.environment, Platform: asked.Platform,
		Label: collapsed(asked.Label, labelMax), Language: collapsed(language, languageMax),
		AppVersion: asked.AppVersion, UpdateID: updateID, RuntimeVersion: asked.RuntimeVersion, FamilyID: family, Now: now,
	})
	if err != nil {
		r.fail(ctx, "could not register the phone", err)
		return
	}
	send(ctx, deviceOut{
		ID: device.ID, Token: device.Token, Kind: device.Kind, Platform: device.Platform,
		Label: device.Label, Language: device.Language, AppVersion: device.AppVersion,
		UpdateID: device.UpdateID, RuntimeVersion: device.RuntimeVersion, LastSeenAt: device.LastSeenAt.UTC(),
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
	if isAPNsToken(strings.ToLower(token)) {
		// Stored in lower case (checkToken).
		token = strings.ToLower(token)
	}
	if isExpoToken(token) || isAPNsToken(token) {
		if err := New(r.Database).RemoveDevice(ctx.Context(), RemoveDeviceParams{UserID: userID, Token: token}); err != nil {
			r.fail(ctx, "could not forget the phone", err)
			return
		}
	}
	ctx.SetHeader("Cache-Control", "no-store")
	ctx.SetStatus(http.StatusNoContent)
}

type preferencesOut struct {
	Choices map[string]bool   `json:"choices"`
	Kinds   []pushpolicy.Kind `json:"kinds"`
	// QuietHours is null when the person has none.
	QuietHours *quietHours `json:"quiet_hours"`
}

// quietHours is the span as the clients read and write it: "HH:MM" on the
// person's own clock, in the IANA zone they chose.
type quietHours struct {
	Start    string `json:"start"`
	End      string `json:"end"`
	TimeZone string `json:"time_zone"`
}

// answer is the stored row as the clients read it. A row with no quiet
// hours, or none at all, is every kind on and no quiet hours.
func answer(choices []byte, start, end *int16, zone *string) preferencesOut {
	out := preferencesOut{Choices: pushpolicy.Choices(choices), Kinds: Kinds}
	if start != nil && end != nil && zone != nil {
		out.QuietHours = &quietHours{Start: pushpolicy.FormatClock(int(*start)), End: pushpolicy.FormatClock(int(*end)), TimeZone: *zone}
	}
	return out
}

func (r Routes) readPreferences(ctx huma.Context, userID string) {
	saved, err := New(r.Database).Preferences(ctx.Context(), userID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		r.fail(ctx, "could not read the push choices", err)
		return
	}
	send(ctx, answer(saved.Choices, saved.QuietStartMinute, saved.QuietEndMinute, saved.TimeZone))
}

// savePreferences changes what the request names and keeps the rest: the
// kinds it names, and the quiet hours when it names them (null switches
// them off). A kind this tier does not know is refused, not stored: it
// would never be read. A span that cannot mean one thing (a time that is
// not "HH:MM", start equal to end, a zone the time zone database does not
// know) is refused, not guessed at.
func (r Routes) savePreferences(ctx huma.Context, userID string) {
	body, ok := readBody(ctx)
	if !ok {
		r.Errors.Write(humawriter.New(ctx), http.StatusRequestEntityTooLarge, "The choices are too large.", requestcontext.ID(ctx.Context()))
		return
	}
	var asked map[string]json.RawMessage
	if err := json.Unmarshal(body, &asked); err != nil || asked == nil {
		r.invalid(ctx, "choices", "Input should be an object with choices, quiet_hours or both")
		return
	}
	rawChoices, hasChoices := asked["choices"]
	rawQuiet, hasQuiet := asked["quiet_hours"]
	if !hasChoices && !hasQuiet {
		r.invalid(ctx, "choices", "Name choices, quiet_hours or both")
		return
	}
	choices := map[string]bool{}
	if hasChoices {
		if err := json.Unmarshal(rawChoices, &choices); err != nil || choices == nil {
			r.invalid(ctx, "choices", "Input should be an object of switches, one per kind")
			return
		}
		for key := range choices {
			if !pushpolicy.Known(key) {
				r.invalid(ctx, "choices", "Not a kind of push: "+key)
				return
			}
		}
	}
	params := SavePreferencesParams{UserID: userID, SetQuiet: hasQuiet, Now: r.now()}
	if hasQuiet && strings.TrimSpace(string(rawQuiet)) != "null" {
		var span quietHours
		if err := json.Unmarshal(rawQuiet, &span); err != nil {
			r.invalid(ctx, "quiet_hours", "Input should be {start, end, time_zone} or null")
			return
		}
		start, startErr := pushpolicy.ParseClock(span.Start)
		end, endErr := pushpolicy.ParseClock(span.End)
		if startErr != nil || endErr != nil {
			r.invalid(ctx, "quiet_hours", pushpolicy.ErrClock.Error())
			return
		}
		if _, err := pushpolicy.NewQuietHours(start, end, span.TimeZone); err != nil {
			r.invalid(ctx, "quiet_hours", err.Error())
			return
		}
		startMinute, endMinute := int16(start), int16(end)
		params.QuietStart, params.QuietEnd, params.TimeZone = &startMinute, &endMinute, &span.TimeZone
	}
	changes, err := json.Marshal(choices)
	if err != nil {
		r.fail(ctx, "could not write the choices", err)
		return
	}
	params.Choices = changes
	saved, err := New(r.Database).SavePreferences(ctx.Context(), params)
	if err != nil {
		r.fail(ctx, "could not save the push choices", err)
		return
	}
	send(ctx, answer(saved.Choices, saved.QuietStartMinute, saved.QuietEndMinute, saved.TimeZone))
}

// send writes a JSON answer nobody caches: it is one account's.
func send(ctx huma.Context, value any) {
	response := humawriter.New(ctx)
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(value)
}
