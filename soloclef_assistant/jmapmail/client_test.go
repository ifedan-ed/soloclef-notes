package jmapmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func fixtureClient(f *fixtures) Client {
	return Client{Network: f, Timeout: 20 * time.Second, AttachmentTimeout: 30 * time.Second}
}

// findInvoice is the test mail's first message: Carol's HTML invoice with
// an attachment and a prompt injection in it. Her reminder is in the same
// thread.
func findInvoice(t *testing.T, client Client, session Session, credential Credential) Summary {
	t.Helper()
	found, _, err := client.Search(context.Background(), session, credential, Query{Subject: "Invoice for October", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range found {
		if summary.Subject == "Invoice for October" {
			return summary
		}
	}
	t.Fatalf("the invoice is not among %+v", found)
	return Summary{}
}

func TestFixtureSessionFollowsTheWellKnownRedirect(t *testing.T) {
	f := newFixtures(t, "session")
	client := fixtureClient(f)
	session, err := client.Session(context.Background(), fixtureOrigin+"/some/path?x=1", f.credential("reader"))
	if err != nil {
		t.Fatal(err)
	}
	if session.Origin != fixtureOrigin || session.APIURL != fixtureOrigin+"/jmap/" || session.AccountID == "" ||
		session.Username != "ben@example.test" || !session.CanSend || session.MaxObjectsInGet != 500 {
		t.Fatalf("session = %+v", session)
	}
	if !strings.HasPrefix(session.DownloadURL, fixtureOrigin+"/jmap/download/{accountId}/{blobId}/{name}") {
		t.Fatalf("the download template lost its placeholders: %q", session.DownloadURL)
	}
	// What a draft's files are checked against: the server's own limits.
	if session.UploadURL != fixtureOrigin+"/jmap/upload/{accountId}/" || session.MaxSizeUpload != 50_000_000 ||
		session.MaxSizeAttachmentsPerEmail != 50_000_000 || session.MaxCallsInRequest != 16 {
		t.Fatalf("the session's limits = %+v", session)
	}
	// /.well-known/jmap first, then the session the redirect named, both
	// signed in.
	if len(f.sent) != 2 || f.sent[0].Path != "/.well-known/jmap" || f.sent[1].Path != "/jmap/session" {
		t.Fatalf("requests = %+v", f.sent)
	}
	for _, sent := range f.sent {
		if !strings.HasPrefix(sent.Authorization, "Basic ") {
			t.Fatalf("a request went without the credential: %+v", sent)
		}
	}
}

func TestFixtureSearchFiltersAndKeepsTheServersOrder(t *testing.T) {
	f := newFixtures(t, "search")
	client := fixtureClient(f)
	credential := f.credential("reader")
	session, err := client.Session(context.Background(), fixtureOrigin, credential)
	if err != nil {
		t.Fatal(err)
	}
	mailboxes, err := client.Mailboxes(context.Background(), session, credential)
	if err != nil {
		t.Fatal(err)
	}
	inbox, found := Named(mailboxes, "INBOX")
	if !found || inbox.Role != "inbox" {
		t.Fatalf("no inbox among %+v", mailboxes)
	}
	if _, found := Named(mailboxes, " sent items "); !found {
		t.Fatalf("a folder is not found by its name: %+v", mailboxes)
	}
	since := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	got, more, err := client.Search(context.Background(), session, credential,
		Query{MailboxID: inbox.ID, From: "carol@example.net", After: since, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Two of Carol's messages match and one is asked for: the newest, and
	// "more".
	if len(got) != 1 || !more || got[0].Subject != "Re: Invoice for October" || !got[0].Unread ||
		got[0].From != "Carol Example <carol@example.net>" || got[0].ThreadID == "" {
		t.Fatalf("search = %+v, more %v", got, more)
	}
	calls := f.calls(4)
	var query map[string]any
	_ = json.Unmarshal(calls["q"][1], &query)
	filter, _ := query["filter"].(map[string]any)
	if filter["inMailbox"] != inbox.ID || filter["from"] != "carol@example.net" || filter["after"] != "2026-11-01T00:00:00Z" ||
		filter["text"] != nil || query["limit"] != float64(1) {
		t.Fatalf("the query sent = %s", calls["q"][1])
	}
	var get map[string]any
	_ = json.Unmarshal(calls["g"][1], &get)
	if _, byReference := get["#ids"]; !byReference {
		t.Fatalf("the summaries were not fetched by back-reference: %s", calls["g"][1])
	}
}

func TestFixtureReadTurnsHTMLIntoTextAndListsAttachments(t *testing.T) {
	f := newFixtures(t, "read")
	client := fixtureClient(f)
	credential := f.credential("reader")
	session, err := client.Session(context.Background(), fixtureOrigin, credential)
	if err != nil {
		t.Fatal(err)
	}
	invoice := findInvoice(t, client, session, credential)
	email, err := client.Read(context.Background(), session, credential, invoice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(email.Body, "**invoice**") || !strings.Contains(email.Body, "[Pay here](https://example.net/pay)") ||
		strings.Contains(email.Body, "<p>") {
		t.Fatalf("the HTML body is not Markdown: %q", email.Body)
	}
	if len(email.Attachments) != 1 || email.Attachments[0].Name != "invoice.txt" || email.Attachments[0].BlobID == "" {
		t.Fatalf("attachments = %+v", email.Attachments)
	}
	if len(email.MessageID) != 1 || email.MessageID[0] != "invoice-1@example.net" || len(email.Cc) != 1 ||
		email.Cc[0].Email != "dave@example.net" || email.SentAt.IsZero() || !email.Unread {
		t.Fatalf("headers = %+v", email)
	}
	// Reading is a /get and nothing else: no keyword set, no message moved.
	for n := 3; n <= len(f.sent); n++ {
		for id, call := range f.calls(n) {
			var name string
			_ = json.Unmarshal(call[0], &name)
			if strings.HasSuffix(name, "/set") {
				t.Fatalf("a read made the write %s (%s)", name, id)
			}
		}
	}
	text, err := client.Download(context.Background(), session, credential, email.Attachments[0], 1<<20)
	if err != nil || strings.TrimSpace(string(text)) != "Amount due: 120 EUR" {
		t.Fatalf("download = %q, %v", text, err)
	}
	if last := f.sent[len(f.sent)-1].Path; !strings.HasPrefix(last, "/jmap/download/") || !strings.Contains(last, "invoice.txt") {
		t.Fatalf("download path = %q", last)
	}
	if _, err := client.Read(context.Background(), session, credential, "no-such-message"); !errors.Is(err, errNotFound) {
		t.Fatalf("an unknown id: %v", err)
	}
}

func TestFixtureThreadIsOldestFirst(t *testing.T) {
	f := newFixtures(t, "thread")
	client := fixtureClient(f)
	credential := f.credential("reader")
	session, err := client.Session(context.Background(), fixtureOrigin, credential)
	if err != nil {
		t.Fatal(err)
	}
	invoice := findInvoice(t, client, session, credential)
	thread, more, err := client.Thread(context.Background(), session, credential, invoice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if more || len(thread) != 2 || thread[0].Subject != "Invoice for October" || thread[1].Subject != "Re: Invoice for October" ||
		thread[1].InReplyTo[0] != "invoice-1@example.net" {
		t.Fatalf("thread = %+v, more %v", thread, more)
	}
}

func TestFixtureSendCreatesSubmitsAndFilesInSent(t *testing.T) {
	f := newFixtures(t, "send")
	client := fixtureClient(f)
	credential := f.credential("sender")
	session, err := client.Session(context.Background(), fixtureOrigin, credential)
	if err != nil {
		t.Fatal(err)
	}
	identities, err := client.Identities(context.Background(), session, credential)
	if err != nil || len(identities) == 0 {
		t.Fatalf("identities = %+v, %v", identities, err)
	}
	me := identities[0]
	id, err := client.Send(context.Background(), session, credential, Outgoing{
		IdentityID: me.ID, From: Address{Name: "Ada", Email: me.Email},
		To: []Address{{Email: "ben@example.test"}}, Bcc: []Address{{Email: "admin@example.test"}},
		Subject: "Fixture send", Body: "Hello Ben,\nthis was sent by the fixture test.\n",
		InReplyTo: []string{"invoice-1@example.net"}, References: []string{"invoice-1@example.net"},
	})
	if err != nil || id == "" {
		t.Fatalf("send = %q, %v", id, err)
	}
	calls := f.calls(len(f.sent))
	var create, submit map[string]any
	_ = json.Unmarshal(calls["e"][1], &create)
	_ = json.Unmarshal(calls["s"][1], &submit)
	email := create["create"].(map[string]any)["draft"].(map[string]any)
	if email["subject"] != "Fixture send" || email["keywords"].(map[string]any)["$draft"] != true || email["bcc"] == nil ||
		email["bodyValues"].(map[string]any)["body"].(map[string]any)["value"] != "Hello Ben,\nthis was sent by the fixture test.\n" {
		t.Fatalf("the message created = %s", calls["e"][1])
	}
	onSuccess := submit["onSuccessUpdateEmail"].(map[string]any)["#send"].(map[string]any)
	if _, cleared := onSuccess["keywords/$draft"]; !cleared || len(onSuccess) != 3 {
		t.Fatalf("the sent copy is not moved out of Drafts: %v", onSuccess)
	}
}

func TestFixtureSendRefusesAnIdentityThatIsNotTheDraftsSender(t *testing.T) {
	f := newFixtures(t, "send_identity")
	client := fixtureClient(f)
	credential := f.credential("sender")
	session, err := client.Session(context.Background(), fixtureOrigin, credential)
	if err != nil {
		t.Fatal(err)
	}
	identities, err := client.Identities(context.Background(), session, credential)
	if err != nil || len(identities) == 0 {
		t.Fatalf("identities = %+v, %v", identities, err)
	}
	_, err = client.Send(context.Background(), session, credential, Outgoing{
		IdentityID: identities[0].ID, From: Address{Email: "someone-else@example.test"},
		To: []Address{{Email: "ben@example.test"}}, Subject: "never", Body: "never",
	})
	var failure *Failure
	if !errors.As(err, &failure) || !strings.Contains(failure.Message, "no longer one this mailbox may send as") {
		t.Fatalf("a changed sender: %v", err)
	}
	// Nothing was created: the identity check is the first request after
	// the session, and the create is never sent.
	for n := 3; n <= len(f.sent); n++ {
		if _, created := f.calls(n)["e"]; created {
			t.Fatal("a message was created for a sender that did not match")
		}
	}
}

// stub is a JMAP server that answers each request with the next of its
// replies, for the cases a real server is not asked to produce.
type stub struct {
	replies []exchangeFixture
	sent    []sentRequest
}

func (s *stub) Client(string) (*http.Client, error) {
	return &http.Client{Transport: s, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

func (s *stub) RoundTrip(request *http.Request) (*http.Response, error) {
	var body []byte
	if request.Body != nil {
		body, _ = io.ReadAll(request.Body)
	}
	s.sent = append(s.sent, sentRequest{Method: request.Method, Path: request.URL.RequestURI(), Body: body})
	next := s.replies[0]
	s.replies = s.replies[1:]
	header := http.Header{}
	if next.Location != "" {
		header.Set("Location", next.Location)
	}
	return &http.Response{StatusCode: next.Status, Header: header, Body: io.NopCloser(bytes.NewReader(next.Body)), Request: request}, nil
}

const stubSession = `{"capabilities":{"urn:ietf:params:jmap:core":{"maxObjectsInGet":500},"urn:ietf:params:jmap:mail":{},"urn:ietf:params:jmap:submission":{}},
 "accounts":{"A":{"name":"ada@example.test","accountCapabilities":{"urn:ietf:params:jmap:mail":{},"urn:ietf:params:jmap:submission":{}}}},
 "primaryAccounts":{"urn:ietf:params:jmap:mail":"A"},"username":"ada@example.test","apiUrl":"/jmap/","downloadUrl":"/jmap/download/{accountId}/{blobId}/{name}?accept={type}"}`

func TestSessionRefusesAnAPIOnAnotherServer(t *testing.T) {
	server := &stub{replies: []exchangeFixture{{Status: 200, Body: json.RawMessage(strings.Replace(stubSession, `"/jmap/"`, `"https://elsewhere.example/jmap/"`, 1))}}}
	_, err := Client{Network: server, Timeout: time.Second}.Session(context.Background(), "https://mail.example.test", Credential{AccessToken: "t"})
	var failure *Failure
	if !errors.As(err, &failure) || !strings.Contains(failure.Message, "calls itself https://elsewhere.example") ||
		failure.Elsewhere != "https://elsewhere.example" {
		t.Fatalf("an API on another server: %v (%q)", err, failure.Elsewhere)
	}
	// A redirect off the origin is not followed either.
	server = &stub{replies: []exchangeFixture{{Status: 307, Location: "https://elsewhere.example/jmap/session"}}}
	if _, err := (Client{Network: server, Timeout: time.Second}).Session(context.Background(), "https://mail.example.test", Credential{AccessToken: "t"}); err == nil || len(server.sent) != 1 {
		t.Fatalf("a redirect to another server: %v after %d requests", err, len(server.sent))
	}
}

func TestSessionWordsWhatIsWrong(t *testing.T) {
	for name, reply := range map[string]exchangeFixture{
		"refused":  {Status: 401},
		"not jmap": {Status: 404},
		"no mail":  {Status: 200, Body: json.RawMessage(`{"capabilities":{"urn:ietf:params:jmap:core":{}},"accounts":{},"primaryAccounts":{}}`)},
	} {
		server := &stub{replies: []exchangeFixture{reply}}
		_, err := Client{Network: server, Timeout: time.Second}.Session(context.Background(), "https://mail.example.test", Credential{AccessToken: "t"})
		want := map[string]error{"refused": errSignInRefused, "not jmap": errNotJMAP, "no mail": errNoMail}[name]
		if !errors.Is(err, want) {
			t.Errorf("%s: %v", name, err)
		}
		if name == "refused" && !IsSignInRefused(err) {
			t.Errorf("a 401 is not a refused sign-in")
		}
	}
}

func TestSendRemovesTheDraftWhenTheSubmissionIsRefused(t *testing.T) {
	server := &stub{replies: []exchangeFixture{
		{Status: 200, Body: json.RawMessage(stubSession)},
		{Status: 200, Body: json.RawMessage(`{"methodResponses":[["Mailbox/get",{"list":[{"id":"d","name":"Drafts","role":"drafts"},{"id":"s","name":"Sent","role":"sent"}]},"m"],["Identity/get",{"list":[{"id":"i1","email":"ada@example.test"}]},"i"]]}`)},
		{Status: 200, Body: json.RawMessage(`{"methodResponses":[["Email/set",{"created":{"draft":{"id":"E1"}}},"e"],["EmailSubmission/set",{"notCreated":{"send":{"type":"forbiddenToSend","description":"missing emailSend"}}},"s"]]}`)},
		{Status: 200, Body: json.RawMessage(`{"methodResponses":[["Email/set",{"destroyed":["E1"]},"d"]]}`)},
	}}
	client := Client{Network: server, Timeout: time.Second}
	session, err := client.Session(context.Background(), "https://mail.example.test", Credential{Username: "ada", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Send(context.Background(), session, Credential{Username: "ada", Password: "p"}, Outgoing{
		IdentityID: "i1", From: Address{Email: "ADA@example.test"}, To: []Address{{Email: "ben@example.test"}}, Subject: "s", Body: "b"})
	var failure *Failure
	if !errors.As(err, &failure) || !strings.Contains(failure.Message, "does not allow this sign-in to send") {
		t.Fatalf("a refused submission: %v", err)
	}
	if len(server.sent) != 4 || !strings.Contains(string(server.sent[3].Body), `"destroy":["E1"]`) {
		t.Fatalf("the draft the failed send made was not removed: %d requests", len(server.sent))
	}
}

func TestMethodErrorsAreWordedAndBounded(t *testing.T) {
	failure := refusal("serverFail", "line one\nline two\x00"+strings.Repeat("x", 500))
	if strings.ContainsAny(failure.Message, "\n\x00") || len([]rune(failure.Message)) > 260 {
		t.Fatalf("a server's description is shown as it came: %q", failure.Message)
	}
	if refusal("forbidden", "").Message != "The mail server does not allow this for this sign-in" {
		t.Fatal("a known type is not worded")
	}
}
