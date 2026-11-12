package apphandoff

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/appreturn"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/driveconnect"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

const (
	alice = "11111111-1111-4111-8111-111111111111"
	bob   = "22222222-2222-4222-8222-222222222222"
)

// The app's PKCE pair (RFC 7636 appendix B's example verifier).
const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"

func challengeOf(v string) string {
	digest := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// memory is Valkey for the tests: SET NX with an expiry, and GETDEL.
type memory struct {
	mu      sync.Mutex
	now     time.Time
	entries map[string]entry
}

type entry struct {
	value   string
	expires time.Time
}

func newMemory() *memory { return &memory{now: time.Now(), entries: map[string]entry{}} }

func (m *memory) SetNew(_ context.Context, key, value string, lifetime time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if held, ok := m.entries[key]; ok && m.now.Before(held.expires) {
		return false, nil
	}
	m.entries[key] = entry{value: value, expires: m.now.Add(lifetime)}
	return true, nil
}

func (m *memory) Take(_ context.Context, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	held, ok := m.entries[key]
	delete(m.entries, key)
	if !ok || !m.now.Before(held.expires) {
		return "", false, nil
	}
	return held.value, true, nil
}

func (m *memory) pass(d time.Duration) {
	m.mu.Lock()
	m.now = m.now.Add(d)
	m.mu.Unlock()
}

// down is a Valkey that does not answer.
type down struct{}

func (down) SetNew(context.Context, string, string, time.Duration) (bool, error) {
	return false, errors.New("connection refused")
}
func (down) Take(context.Context, string) (string, bool, error) {
	return "", false, errors.New("connection refused")
}

// drive stands in for internal/driveconnect, per account.
type drive struct {
	configured bool
	// connections is each account's connection id; reconnect, those
	// Google refuses.
	connections map[string]int64
	reconnect   map[string]bool
	begun       []string // "user challenge" of each BeginForApp
	saved       []string // "user sealed" of each SaveForApp
}

func (d *drive) Configured() bool { return d.configured }

func (d *drive) UsableConnection(_ context.Context, owner string) (int64, error) {
	id, ok := d.connections[owner]
	switch {
	case !ok:
		return 0, driveconnect.ErrNotConnected
	case d.reconnect[owner]:
		return 0, driveconnect.ErrReconnect
	}
	return id, nil
}

func (d *drive) PickerFor(ctx context.Context, _, owner string) (driveconnect.PickerSession, error) {
	id, err := d.UsableConnection(ctx, owner)
	if err != nil {
		return driveconnect.PickerSession{}, err
	}
	return driveconnect.PickerSession{ConnectorID: id, AccessToken: "picker-token-of-" + owner, APIKey: "browser-key", AppID: "123456"}, nil
}

func (d *drive) BeginForApp(response http.ResponseWriter, owner, challenge string) (string, error) {
	d.begun = append(d.begun, owner+" "+challenge)
	http.SetCookie(response, &http.Cookie{Name: "notes_drive_connect", Value: "sealed-attempt"})
	return "https://accounts.google.test/auth?state=s", nil
}

func (d *drive) SaveForApp(_ context.Context, _, owner, sealed string) (int64, error) {
	d.saved = append(d.saved, owner+" "+sealed)
	return 7, nil
}

// microsoft stands in for internal/microsoftmail.
type microsoft struct {
	configured bool
	begun      []string // "user account challenge"
	saved      []string
}

func (m *microsoft) Configured() bool { return m.configured }

func (m *microsoft) BeginForApp(_ context.Context, _ http.ResponseWriter, owner, account, challenge string) (string, error) {
	m.begun = append(m.begun, owner+" "+account+" "+challenge)
	return "https://login.microsoft.test/authorize", nil
}

func (m *microsoft) SaveForApp(_ context.Context, _, owner, sealed string) error {
	m.saved = append(m.saved, owner+" "+sealed)
	return nil
}

// importer records the imports it was asked for.
type importer struct{ asked []string }

func (i *importer) ImportFor(ctx huma.Context, user string, connector int64, body io.Reader) {
	raw, _ := io.ReadAll(body)
	i.asked = append(i.asked, user+" "+string(raw)+" via "+itoa(connector))
	ctx.SetStatus(http.StatusOK)
	_, _ = ctx.BodyWriter().Write([]byte(`{"document_ids":[1],"queued":1,"unsupported":[]}`))
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

// notebooks: Alice owns 10, Bob 20.
type notebooks struct{}

func (notebooks) OwnsNotebook(_ context.Context, user string, id int64) (bool, error) {
	return (user == alice && id == 10) || (user == bob && id == 20), nil
}

type accounts struct{ inactive map[string]bool }

func (a accounts) UserIsActive(_ context.Context, user string) (bool, error) {
	return !a.inactive[user], nil
}

type site struct {
	t         *testing.T
	store     *memory
	drive     *drive
	microsoft *microsoft
	imports   *importer
	accounts  accounts
	handler   http.Handler
}

func newSite(t *testing.T) *site {
	t.Helper()
	s := &site{t: t, store: newMemory(),
		drive:     &drive{configured: true, connections: map[string]int64{alice: 1, bob: 2}, reconnect: map[string]bool{}},
		microsoft: &microsoft{configured: true}, imports: &importer{}, accounts: accounts{inactive: map[string]bool{}}}
	s.build(s.store)
	return s
}

func (s *site) build(store KeyValue) {
	routes := Routes{Secrets: Secrets{Store: store}, Drive: s.drive, Microsoft: s.microsoft, Imports: s.imports,
		Notebooks: notebooks{}, Accounts: s.accounts, Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := routes.Check(); err != nil {
		s.t.Fatal(err)
	}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(api)
	s.handler = api.Adapter()
}

// call sends a request as caller ("" for nobody, an API key for "pat:").
func (s *site) call(method, path, body, caller string) *httptest.ResponseRecorder {
	s.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if caller != "" {
		how := principal.MethodSession
		if strings.HasPrefix(caller, "pat:") {
			caller, how = strings.TrimPrefix(caller, "pat:"), principal.MethodPersonalAccessToken
		}
		request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: caller, Method: how}))
	}
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, request)
	return recorder
}

// refusal is an error envelope's status and code.
func refusal(t *testing.T, got *httptest.ResponseRecorder) (int, string) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code   string
			Status int
		}
	}
	if err := json.Unmarshal(got.Body.Bytes(), &envelope); err != nil || envelope.Error.Status != got.Code {
		t.Fatalf("not the error envelope: %d %s", got.Code, got.Body)
	}
	return got.Code, envelope.Error.Code
}

type started struct {
	Code      string `json:"code"`
	ExpiresIn int    `json:"expires_in"`
	Path      string `json:"path"`
}

func (s *site) start(caller, body string) started {
	s.t.Helper()
	got := s.call(http.MethodPost, StartPath, body, caller)
	if got.Code != http.StatusOK {
		s.t.Fatalf("start %s: %d %s", body, got.Code, got.Body)
	}
	if got.Header().Get("Cache-Control") != "no-store" {
		s.t.Fatalf("a code was answered cacheable: %q", got.Header().Get("Cache-Control"))
	}
	var answer started
	if err := json.Unmarshal(got.Body.Bytes(), &answer); err != nil {
		s.t.Fatal(err)
	}
	return answer
}

func TestAStartIsRefusedForWhatTheWebsiteWouldRefuse(t *testing.T) {
	withChallenge := `,"code_challenge":"` + challengeOf(verifier) + `"`
	for _, c := range []struct {
		name, caller, body string
		setup              func(*site)
		status             int
		code               string
	}{
		{"an API key", "pat:" + alice, `{"purpose":"drive_connect"` + withChallenge + `}`, nil, 403, "FORBIDDEN"},
		{"not JSON", alice, `purpose=drive_connect`, nil, 422, ""},
		{"a workspace, which no longer exists", alice, `{"purpose":"drive_picker","workspace_id":3}`, nil, 422, ""},
		{"no purpose", alice, `{}`, nil, 422, ""},
		{"a purpose that is not one", alice, `{"purpose":"dropbox"}`, nil, 400, codeUnsupported},
		{"a challenge that is not S256", alice, `{"purpose":"drive_connect","code_challenge":"short"}`, nil, 422, ""},
		{"Drive connect without a challenge", alice, `{"purpose":"drive_connect"}`, nil, 400, codeChallengeRequired},
		{"Microsoft without a challenge", alice, `{"purpose":"microsoft_mail"}`, nil, 400, codeChallengeRequired},
		{"Drive not set up", alice, `{"purpose":"drive_connect"}`, func(s *site) { s.drive.configured = false }, 503, codeDriveNotConfigured},
		{"Microsoft not set up", alice, `{"purpose":"microsoft_mail"` + withChallenge + `}`, func(s *site) { s.microsoft.configured = false }, 503, codeMicrosoftNotConfigured},
		{"the Picker without a connection", alice, `{"purpose":"drive_picker"}`, func(s *site) { delete(s.drive.connections, alice) }, 404, codeDriveNotConnected},
		{"the Picker on a refused grant", alice, `{"purpose":"drive_picker"}`, func(s *site) { s.drive.reconnect[alice] = true }, 400, codeDriveReconnect},
		{"the Picker into another account's notebook", bob, `{"purpose":"drive_picker","notebook_id":10}`, nil, 404, "NOT_FOUND"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSite(t)
			if c.setup != nil {
				c.setup(s)
			}
			got := s.call(http.MethodPost, StartPath, c.body, c.caller)
			status, code := refusal(t, got)
			if status != c.status || (c.code != "" && code != c.code) {
				t.Fatalf("got %d %s, want %d %s: %s", status, code, c.status, c.code, got.Body)
			}
			if len(s.store.entries) != 0 {
				t.Fatal("a refused start issued a code")
			}
		})
	}
}

func TestACodeIsKeptHashedAndOpensOnce(t *testing.T) {
	s := newSite(t)
	got := s.start(alice, `{"purpose":"drive_connect","code_challenge":"`+challengeOf(verifier)+`"}`)
	if got.ExpiresIn != 60 || got.Path != "/api/v1/app/handoff/"+got.Code || len(got.Code) != 43 {
		t.Fatalf("started %+v", got)
	}
	for key, held := range s.store.entries {
		if strings.Contains(key, got.Code) || strings.Contains(held.value, got.Code) {
			t.Fatal("the code itself is in Valkey")
		}
	}
	first := s.call(http.MethodGet, got.Path, "", "")
	if first.Code != http.StatusSeeOther || first.Header().Get("Location") != "https://accounts.google.test/auth?state=s" {
		t.Fatalf("the page: %d %q", first.Code, first.Header().Get("Location"))
	}
	if first.Header().Get("Referrer-Policy") != "no-referrer" || first.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(first.Header().Get("Set-Cookie"), "notes_drive_connect") {
		t.Fatalf("the page's headers: %v", first.Header())
	}
	if len(s.drive.begun) != 1 || s.drive.begun[0] != alice+" "+challengeOf(verifier) {
		t.Fatalf("the connect began as %v", s.drive.begun)
	}
	again := s.call(http.MethodGet, got.Path, "", "")
	if again.Header().Get("Location") != appreturn.Connectors+"?status=expired" {
		t.Fatalf("a spent code opened: %q", again.Header().Get("Location"))
	}
}

func TestACodeRunsOutAfterAMinute(t *testing.T) {
	s := newSite(t)
	got := s.start(alice, `{"purpose":"drive_picker","notebook_id":10}`)
	if !strings.HasSuffix(got.Path, "?for=drive_picker") {
		t.Fatalf("the Picker's path names no purpose: %q", got.Path)
	}
	s.store.pass(61 * time.Second)
	page := s.call(http.MethodGet, got.Path, "", "")
	if page.Code != http.StatusGone || !strings.Contains(page.Body.String(), "This link has run out") {
		t.Fatalf("an old Picker code: %d %s", page.Code, page.Body)
	}
}

func TestMicrosoftReconnectNamesTheMailbox(t *testing.T) {
	s := newSite(t)
	got := s.start(alice, `{"purpose":"microsoft_mail","account_id":"box-1","code_challenge":"`+challengeOf(verifier)+`"}`)
	page := s.call(http.MethodGet, got.Path, "", "")
	if page.Code != http.StatusSeeOther || page.Header().Get("Location") != "https://login.microsoft.test/authorize" {
		t.Fatalf("the page: %d %q", page.Code, page.Header().Get("Location"))
	}
	if len(s.microsoft.begun) != 1 || s.microsoft.begun[0] != alice+" box-1 "+challengeOf(verifier) {
		t.Fatalf("began %v", s.microsoft.begun)
	}
}

func TestAProviderSetUpNoLongerSendsTheSheetBack(t *testing.T) {
	s := newSite(t)
	got := s.start(alice, `{"purpose":"drive_connect","code_challenge":"`+challengeOf(verifier)+`"}`)
	s.drive.configured = false
	page := s.call(http.MethodGet, got.Path, "", "")
	if page.Header().Get("Location") != appreturn.ConnectorReturn(appreturn.DriveConnect, appreturn.StatusNotConfigured, "") {
		t.Fatalf("went to %q", page.Header().Get("Location"))
	}
}

var ticketInPage = regexp.MustCompile(`"ticket":"([^"]+)"`)

// pickerPage opens the Picker's page for caller and answers its ticket.
func (s *site) pickerPage(caller string, notebook string) (string, *httptest.ResponseRecorder) {
	s.t.Helper()
	body := `{"purpose":"drive_picker"`
	if notebook != "" {
		body += `,"notebook_id":` + notebook
	}
	got := s.start(caller, body+`}`)
	page := s.call(http.MethodGet, got.Path, "", "")
	found := ticketInPage.FindStringSubmatch(page.Body.String())
	if page.Code != http.StatusOK || found == nil {
		return "", page
	}
	return found[1], page
}

func TestThePickerPageCarriesItsTokenInItsBodyOnly(t *testing.T) {
	s := newSite(t)
	ticket, page := s.pickerPage(alice, "10")
	if ticket == "" {
		t.Fatalf("no Picker page: %d %s", page.Code, page.Body)
	}
	policy := page.Header().Get("Content-Security-Policy")
	nonce := regexp.MustCompile(`'nonce-([^']+)'`).FindStringSubmatch(policy)
	if nonce == nil || !strings.Contains(page.Body.String(), `<script nonce="`+nonce[1]+`">`) ||
		!strings.Contains(policy, "frame-ancestors 'none'") || page.Header().Get("X-Frame-Options") != "DENY" ||
		page.Header().Get("Cache-Control") != "no-store" || page.Header().Get("Referrer-Policy") != "strict-origin" {
		t.Fatalf("the page's protections: %v", page.Header())
	}
	if !strings.Contains(page.Body.String(), `"accessToken":"picker-token-of-`+alice+`"`) ||
		!strings.Contains(page.Body.String(), `"doneUrl":"soloclef://drive/done"`) {
		t.Fatalf("the page's config: %s", page.Body)
	}
}

func TestThePickerGoesBackSayingWhyItCannotOpen(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(*site)
		want  string
	}{
		{"Google refused the grant meanwhile", func(s *site) { s.drive.reconnect[alice] = true }, "google_drive_reconnect"},
		{"the account was deactivated meanwhile", func(s *site) { s.accounts.inactive[alice] = true }, "signed_out"},
		{"the connection was removed meanwhile", func(s *site) { delete(s.drive.connections, alice) }, "picker_failed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSite(t)
			got := s.start(alice, `{"purpose":"drive_picker"}`)
			c.setup(s)
			page := s.call(http.MethodGet, got.Path, "", "")
			if page.Code != http.StatusFound || page.Header().Get("Location") != appreturn.Drive+"?error="+c.want {
				t.Fatalf("got %d %q", page.Code, page.Header().Get("Location"))
			}
		})
	}
}

func TestATicketImportsOnceIntoItsOwnNotebook(t *testing.T) {
	s := newSite(t)
	ticket, page := s.pickerPage(alice, "10")
	if ticket == "" {
		t.Fatalf("no page: %s", page.Body)
	}
	// The page cannot name a notebook of its own: the ticket says which.
	sneaky := s.call(http.MethodPost, ImportPath, `{"ticket":"`+ticket+`","files":[],"notebook_id":20}`, "")
	if status, _ := refusal(t, sneaky); status != 422 {
		t.Fatalf("a notebook in the page's body: %d", status)
	}
	files := `[{"id":"file-1","name":"Plan.pdf","mime_type":"application/pdf"}]`
	got := s.call(http.MethodPost, ImportPath, `{"ticket":"`+ticket+`","files":`+files+`}`, "")
	if got.Code != http.StatusOK || got.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("the import: %d %s", got.Code, got.Body)
	}
	want := alice + ` {"files":` + files + `,"notebook_id":10} via 1`
	if len(s.imports.asked) != 1 || s.imports.asked[0] != want {
		t.Fatalf("imported %v, want %s", s.imports.asked, want)
	}
	again := s.call(http.MethodPost, ImportPath, `{"ticket":"`+ticket+`","files":`+files+`}`, "")
	if status, code := refusal(t, again); status != http.StatusGone || code != codeExpired {
		t.Fatalf("a spent ticket: %d %s", status, code)
	}
}

func TestACodeIsNotATicket(t *testing.T) {
	s := newSite(t)
	got := s.start(alice, `{"purpose":"drive_picker"}`)
	answer := s.call(http.MethodPost, ImportPath, `{"ticket":"`+got.Code+`","files":[{"id":"f","name":"a.pdf"}]}`, "")
	if status, code := refusal(t, answer); status != http.StatusGone || code != codeExpired || len(s.imports.asked) != 0 {
		t.Fatalf("a code imported: %d %s", status, code)
	}
}

// held is a completion a callback would have made for user.
func (s *site) held(purpose, user string) string {
	s.t.Helper()
	completion, err := Secrets{Store: s.store}.Holder(purpose).Hold(context.Background(), user, challengeOf(verifier), "sealed-grant-of-"+user)
	if err != nil {
		s.t.Fatal(err)
	}
	return completion
}

func completeBody(completion, v string) string {
	return `{"completion":"` + completion + `","code_verifier":"` + v + `"}`
}

func TestOnlyTheAccountThatAskedSavesWhatWasHeld(t *testing.T) {
	s := newSite(t)
	completion := s.held(appreturn.DriveConnect, alice)
	// Bob, with Alice's completion and even her verifier: nothing saved,
	// and the completion is spent.
	got := s.call(http.MethodPost, CompletePath, completeBody(completion, verifier), bob)
	if status, code := refusal(t, got); status != http.StatusForbidden || code != codeWrongAccount {
		t.Fatalf("another account: %d %s", status, code)
	}
	again := s.call(http.MethodPost, CompletePath, completeBody(completion, verifier), alice)
	if status, code := refusal(t, again); status != http.StatusGone || code != codeExpired {
		t.Fatalf("a spent completion: %d %s", status, code)
	}
	if len(s.drive.saved) != 0 {
		t.Fatalf("saved %v", s.drive.saved)
	}
}

func TestAWrongVerifierSpendsTheCompletion(t *testing.T) {
	s := newSite(t)
	completion := s.held(appreturn.MicrosoftMail, alice)
	wrong := strings.Repeat("a", 43)
	got := s.call(http.MethodPost, CompletePath, completeBody(completion, wrong), alice)
	if status, code := refusal(t, got); status != http.StatusForbidden || code != codeWrongVerifier {
		t.Fatalf("a wrong verifier: %d %s", status, code)
	}
	again := s.call(http.MethodPost, CompletePath, completeBody(completion, verifier), alice)
	if status, _ := refusal(t, again); status != http.StatusGone || len(s.microsoft.saved) != 0 {
		t.Fatalf("a second try saved: %d %v", status, s.microsoft.saved)
	}
}

func TestTheRightAppSavesWhatWasHeld(t *testing.T) {
	for _, purpose := range []string{appreturn.DriveConnect, appreturn.MicrosoftMail} {
		t.Run(purpose, func(t *testing.T) {
			s := newSite(t)
			completion := s.held(purpose, alice)
			got := s.call(http.MethodPost, CompletePath, completeBody(completion, verifier), alice)
			if got.Code != http.StatusOK || got.Body.String() != `{"purpose":"`+purpose+`","status":"connected"}` {
				t.Fatalf("the completion: %d %s", got.Code, got.Body)
			}
			saved := append(s.drive.saved, s.microsoft.saved...)
			if len(saved) != 1 || saved[0] != alice+" sealed-grant-of-"+alice {
				t.Fatalf("saved %v", saved)
			}
		})
	}
}

func TestACompletionRunsOutAfterFiveMinutes(t *testing.T) {
	s := newSite(t)
	completion := s.held(appreturn.DriveConnect, alice)
	s.store.pass(5*time.Minute + time.Second)
	got := s.call(http.MethodPost, CompletePath, completeBody(completion, verifier), alice)
	if status, code := refusal(t, got); status != http.StatusGone || code != codeExpired {
		t.Fatalf("an old completion: %d %s", status, code)
	}
}

func TestAValkeyThatIsDownIsSaidAndNothingElse(t *testing.T) {
	s := newSite(t)
	s.build(down{})
	got := s.call(http.MethodPost, StartPath, `{"purpose":"drive_picker"}`, alice)
	if status, _ := refusal(t, got); status != http.StatusServiceUnavailable {
		t.Fatalf("start: %d", status)
	}
	page := s.call(http.MethodGet, "/api/v1/app/handoff/abc", "", "")
	if page.Header().Get("Location") != appreturn.ConnectorReturn("", appreturn.StatusFailed, "") {
		t.Fatalf("page: %d %q", page.Code, page.Header().Get("Location"))
	}
}

func TestTheReturnsAreTheAppsOwnAddresses(t *testing.T) {
	back, _ := url.Parse(appreturn.ConnectorReturn(appreturn.DriveConnect, appreturn.StatusConfirm, "c&d"))
	if back.Scheme != "soloclef" || back.Host != "connectors" || back.Path != "/done" ||
		back.Query().Get("purpose") != "drive_connect" || back.Query().Get("status") != "confirm" || back.Query().Get("completion") != "c&d" {
		t.Fatalf("the return: %s", back)
	}
}
