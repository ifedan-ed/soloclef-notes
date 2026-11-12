package signin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Routes is the package's routes and what they share.
type Routes struct {
	Pool *pgxpool.Pool
	// Secret signs every access token; it is the one internal/principal
	// verifies with.
	Secret  string
	Cookies CookiePolicy
	Hasher  *Hasher
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
	// SignupAllowed is the deployment's one answer to "may an account be
	// made by the person themselves": the sign-up form here, and a first
	// single sign-on in internal/oidcsignin, which is given the same value.
	SignupAllowed bool
	// Mail sends the reset link; nil means this deployment sends no mail,
	// and a reset is then refused in so many words (password_reset.go).
	Mail Mailer
	// FrontendURL is the web app's address, where the reset link leads, and
	// BrandName what the mail calls the product. Both are read only with
	// Mail set.
	FrontendURL string
	BrandName   string
	// Background runs what a request leaves to be done after its answer has
	// gone (the reset mail). Nil starts a goroutine; a test runs it inline.
	Background func(task func())
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

// Register mounts every route.
func (r Routes) Register(api huma.API) {
	r.mount(api, http.MethodPost, PasswordPath, "sign-in-web", "Sign in with a password (web)", r.passwordSignIn)
	r.mount(api, http.MethodPost, PhonePath, "sign-in-phone", "Sign in with a password (phone app)", r.phoneSignIn)
	r.mount(api, http.MethodPost, RefreshPath, "refresh-session", "A new access token for a refresh token", r.refresh)
	r.mount(api, http.MethodPost, RevokePath, "sign-out", "Sign this device out", r.signOut)
	r.mount(api, http.MethodGet, SessionPath, "read-session", "When the caller's access token expires", r.session)
	r.mount(api, http.MethodPost, RegisterPath, "sign-up", "Make an account with a password", r.register)
	r.mount(api, http.MethodPost, ForgotPath, "forgot-password", "Mail a link that sets a new password", r.forgotPassword)
	r.mount(api, http.MethodPost, ResetPath, "reset-password", "Set a new password with a mailed link's token", r.resetPassword)
	r.mount(api, http.MethodGet, VerifyPath, "verify-token", "Who the request's credential is", r.verifyToken)
	r.mount(api, http.MethodGet, DevicesPath, "signed-in-devices", "Every device the account is signed in on", r.devices)
	r.mount(api, http.MethodDelete, DevicePath, "sign-out-device", "Sign one device out", r.signOutDevice)
	r.mount(api, http.MethodPost, LogoutAllPath, "sign-out-everywhere", "Sign every device out", r.logoutAll)
}

func (r Routes) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

func (r Routes) mount(api huma.API, method, path, id, summary string, serve http.HandlerFunc) {
	Mount(api, method, path, id, summary, serve)
}

// Mount registers one route as an ordinary http handler. The sign-in routes
// read a form or a small JSON body, cookies and headers, and write cookies
// and redirects: they are written against net/http, and Huma is only the
// router they share with the rest of the tier. Exported for
// internal/oidcsignin, whose two routes are of the same kind.
func Mount(api huma.API, method, path, id, summary string, serve http.HandlerFunc) {
	huma.Register(api, huma.Operation{
		OperationID: id,
		Method:      method,
		Path:        path,
		Summary:     summary,
		Tags:        []string{"sign-in"},
	}, func(_ context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			serve(humawriter.New(ctx), requestOf(ctx))
		}}, nil
	})
}

// requestOf rebuilds the *http.Request a Huma stream handler is not given,
// with the headers, the body and the context (which carries the request id
// and, behind the gate, the caller).
func requestOf(ctx huma.Context) *http.Request {
	address := ctx.URL()
	request, err := http.NewRequestWithContext(ctx.Context(), ctx.Method(), address.String(), ctx.BodyReader())
	if err != nil {
		// The URL came from a request the server already parsed.
		request = &http.Request{Method: ctx.Method(), URL: &address, Header: http.Header{}}
		request = request.WithContext(ctx.Context())
	}
	ctx.EachHeader(func(name, value string) { request.Header.Add(name, value) })
	request.RemoteAddr = ctx.RemoteAddr()
	return request
}

// maxBody bounds what any of these routes reads: an address, a password, a
// device name, a refresh token.
const maxBody = 16 << 10

// readBody is the request's body, at most maxBody bytes.
func readBody(request *http.Request) ([]byte, bool) {
	if request.Body == nil {
		return nil, true
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return nil, false
	}
	return body, true
}

// send writes a JSON answer that no route of this package caches.
func send(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

// refuse answers the tier's error envelope.
func (r Routes) refuse(response http.ResponseWriter, request *http.Request, status int, message string) {
	response.Header().Set("Cache-Control", "no-store")
	r.Errors.Write(response, status, message, requestcontext.IDFrom(request))
}

// fail logs the cause and answers the tier's 500. A cancelled request is not
// a fault and is not logged as one.
func (r Routes) fail(response http.ResponseWriter, request *http.Request, reason string, cause error) {
	if !errors.Is(cause, context.Canceled) {
		r.Logger.ErrorContext(request.Context(), "a sign-in route could not be served",
			slog.String("request_id", requestcontext.IDFrom(request)),
			slog.String("reason", reason), slog.String("error", errString(cause)))
	}
	r.refuse(response, request, http.StatusInternalServerError, errorenvelope.GenericServerMessage)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
