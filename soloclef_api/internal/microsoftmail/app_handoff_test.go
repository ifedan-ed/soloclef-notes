package microsoftmail

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/appreturn"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// holding records what a callback held for the app.
type holding struct{ userID, challenge, sealed string }

func (h *holding) Hold(_ context.Context, userID, challenge, sealed string) (string, error) {
	h.userID, h.challenge, h.sealed = userID, challenge, sealed
	return "completion-1", nil
}

// appSite is the mail site with its routes holding for the app.
func appSite(t *testing.T) (*mailSite, Routes, *holding) {
	t.Helper()
	s := newMailSite(t, true)
	held := &holding{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	routes, err := NewRoutes(Store{Pool: s.pool}, s.settings, http.DefaultTransport, func() time.Time { return s.now },
		errorenvelope.Writer{ReportURL: "https://notes.example/contact"}, logger)
	if err != nil {
		t.Fatal(err)
	}
	routes.AppHold = held
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(api)
	s.handler = requestcontext.Middleware(logger, api.Adapter())
	return s, routes, held
}

const appChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

// beginForApp starts an app's connect as userID, as the hand-off's page
// does, and answers the cookie and state the callback is given.
func beginForApp(t *testing.T, s *mailSite, routes Routes, userID string) (cookie, state string) {
	t.Helper()
	page := httptest.NewRecorder()
	signIn, err := routes.BeginForApp(context.Background(), page, userID, "", appChallenge)
	if err != nil {
		t.Fatal(err)
	}
	consent, err := url.Parse(signIn)
	if err != nil || consent.Host != "login.microsoft.test" || consent.Query().Get("code_challenge") == appChallenge {
		// The app's challenge stays with the hand-off; Microsoft is given
		// the attempt's own.
		t.Fatalf("the sign-in page is %q", signIn)
	}
	s.microsoft.mu.Lock()
	s.microsoft.challenge = consent.Query().Get("code_challenge")
	s.microsoft.mu.Unlock()
	set := page.Result().Cookies()
	if len(set) != 1 || set[0].Name != cookieName {
		t.Fatalf("the attempt's cookie is %+v", set)
	}
	return cookieName + "=" + set[0].Value, consent.Query().Get("state")
}

func TestAnAppsConnectHoldsTheMailboxForTheApp(t *testing.T) {
	s, routes, held := appSite(t)
	cookie, state := beginForApp(t, s, routes, testpostgres.Alice)
	back := s.as("", http.MethodGet, CallbackPath+"?code=good-code&state="+url.QueryEscape(state), "", cookie)
	if back.Code != http.StatusSeeOther ||
		back.Header().Get("Location") != appreturn.ConnectorReturn(appreturn.MicrosoftMail, appreturn.StatusConfirm, "completion-1") {
		t.Fatalf("the callback: %d %q", back.Code, back.Header().Get("Location"))
	}
	if s.connections() != 0 {
		t.Fatal("the callback saved the mailbox; only the app's confirmation may")
	}
	if held.userID != testpostgres.Alice || held.challenge != appChallenge || held.sealed == "" {
		t.Fatalf("held %+v", held)
	}

	// The hand-off has checked who confirms; this saves for that account.
	if err := routes.SaveForApp(context.Background(), "req", testpostgres.Alice, held.sealed); err != nil {
		t.Fatal(err)
	}
	if mine, theirs := s.records(testpostgres.Alice), s.records(testpostgres.Bob); len(mine) != 1 || len(theirs) != 0 ||
		mine[0].Text("username") != "ada@example.test" {
		t.Fatalf("Alice has %v, Bob %v", mine, theirs)
	}
	other := routes
	other.Settings.SecretKey = "another deployment's key"
	if err := other.SaveForApp(context.Background(), "req", testpostgres.Alice, held.sealed); !errors.Is(err, ErrHeldUnreadable) {
		t.Fatalf("a held mailbox opened with another key: %v", err)
	}
}

func TestAnAppsConnectThatMicrosoftRefusedGoesBackToTheApp(t *testing.T) {
	s, routes, held := appSite(t)
	cookie, state := beginForApp(t, s, routes, testpostgres.Alice)
	back := s.as("", http.MethodGet, CallbackPath+"?error=access_denied&state="+url.QueryEscape(state), "", cookie)
	if back.Header().Get("Location") != appreturn.ConnectorReturn(appreturn.MicrosoftMail, outcomeDenied, "") || held.sealed != "" {
		t.Fatalf("the callback: %q, held %+v", back.Header().Get("Location"), held)
	}
}
