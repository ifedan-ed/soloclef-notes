package maildrafts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapaccounts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
)

// The end-to-end test below connects a mailbox on a real Stalwart, reads
// mail, drafts, confirms and sends through the guard, and checks what the
// recipient got. It runs only when it is given a throwaway Stalwart whose
// public address is https://mail.example.test, reachable at a loopback
// address (the test's transport stands in for DNS and TLS), with users
// ada and ben (Ben's inbox holding Carol's invoice and its reply, Ada's a
// message from Carol with statement.pdf attached, each put there with
// Email/import), and a throwaway database:
//
//	JMAP_E2E_STALWART=http://127.0.0.1:PORT \
//	JMAP_E2E_SENDER='ada@example.test:password' JMAP_E2E_RECIPIENT='ben@example.test:password' \
//	SOLOCLEF_API_TEST_MAILDRAFTS_DATABASE=postgres://... \
//	  go test ./maildrafts/ -run EndToEnd -count=1 -v
//
// It sends real mail between those two users (and Cc's a sub-address of
// carol@example.test, on the same server) only, and must never be pointed at a server anybody
// uses. On Stalwart 1.0 a throwaway server is set up over JMAP itself:
// x:Bootstrap/set on the first start, then x:Account/set for the users.

const stalwartOrigin = "https://mail.example.test"

// loopback reaches the test's Stalwart for https://mail.example.test. The
// production network (jmapaccounts.PublicNetwork) refuses exactly this, a
// loopback address, which is why a test hands in its own.
type loopback struct{ target *url.URL }

func (l loopback) Client(string) (*http.Client, error) {
	return &http.Client{
		Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
			if request.URL.Host != "mail.example.test" {
				return nil, errors.New("the test's network reaches mail.example.test only")
			}
			forwarded := request.Clone(request.Context())
			forwarded.URL.Scheme, forwarded.URL.Host = l.target.Scheme, l.target.Host
			forwarded.Host = l.target.Host
			return http.DefaultTransport.RoundTrip(forwarded)
		}),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

type roundTrip func(*http.Request) (*http.Response, error)

func (r roundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return r(request) }

func TestEndToEndAgainstStalwart(t *testing.T) {
	server, sender, recipient := os.Getenv("JMAP_E2E_STALWART"), os.Getenv("JMAP_E2E_SENDER"), os.Getenv("JMAP_E2E_RECIPIENT")
	database := os.Getenv(databaseVariable)
	if server == "" || sender == "" || recipient == "" || database == "" {
		t.Skip("JMAP_E2E_STALWART, JMAP_E2E_SENDER, JMAP_E2E_RECIPIENT and the database are needed")
	}
	target, err := url.Parse(server)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `TRUNCATE "user" CASCADE; INSERT INTO "user" (id, email) VALUES ('`+ownerA+`', 'ada@example.test'), ('`+ownerB+`', 'ben@example.test')`); err != nil {
		t.Fatal(err)
	}
	seal, err := jmapaccounts.NewSeal("an end-to-end test secret")
	if err != nil {
		t.Fatal(err)
	}
	network := loopback{target: target}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mail := &jmapaccounts.Mail{
		SignIn: jmapaccounts.SignIn{Rows: jmapaccounts.Store{Pool: pool}, Seal: seal,
			OAuth: jmapaccounts.OAuth{Network: network, Timeout: 10 * time.Second}, Now: time.Now, Logger: logger},
		Client: jmapmail.Client{Network: network, Timeout: 20 * time.Second, AttachmentTimeout: 30 * time.Second},
	}
	if err := mail.Check(); err != nil {
		t.Fatal(err)
	}
	studio := &memoryFiles{files: map[int64][]byte{1: []byte("PK\x03\x04 the launch slides, as the Studio made them")}, mail: mail}
	jmapSender := JMAPSender{Mail: mail, Files: studio}
	guard := &Guard{Rows: Store{Pool: pool}, Accounts: jmapSender, Senders: map[string]Sender{ProviderJMAP: jmapSender},
		History: jmapSender, Files: studio, Notify: NoNotifications{}, Now: time.Now, Logger: logger}
	if err := guard.Check(); err != nil {
		t.Fatal(err)
	}
	user := func(pair string) (string, string) { name, password, _ := strings.Cut(pair, ":"); return name, password }

	// Stalwart's sign-in, as far as it can go without a public address for
	// the client document: the metadata is usable, and a server that does
	// not accept metadata documents (the default) is caught before the
	// person is sent there.
	metadata, err := mail.SignIn.OAuth.Discover(ctx, stalwartOrigin)
	if err != nil || metadata.TokenEndpoint != stalwartOrigin+"/auth/token" || !metadata.IssuerInResponse {
		t.Fatalf("discover = %+v, %v", metadata, err)
	}
	address := jmapaccounts.AuthorizationRequest(metadata, "https://notes.example.org"+"/api/v1/connectors/jmap/oauth-client.json",
		"https://notes.example.org/api/v1/auth/jmap/mail/connector/callback", "state", "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", "")
	var refused *jmapaccounts.Refused
	if err := mail.SignIn.OAuth.Preflight(ctx, stalwartOrigin, address); !errors.As(err, &refused) || !strings.Contains(refused.Message, "administrator can allow it") {
		t.Fatalf("preflight against a server that refuses metadata documents: %v", err)
	}

	// Ada and Ben connect with app passwords; a wrong one is refused.
	adaName, adaPassword := user(sender)
	benName, benPassword := user(recipient)
	if _, err := mail.ConnectAppPassword(ctx, ownerA, "", stalwartOrigin, adaName, "wrong"); !jmapmail.IsSignInRefused(err) {
		t.Fatalf("a wrong app password: %v", err)
	}
	adaBox, err := mail.ConnectAppPassword(ctx, ownerA, "Ada at Stalwart", stalwartOrigin, adaName, adaPassword)
	if err != nil || !adaBox.CanSend {
		t.Fatalf("Ada's connect = %+v, %v", adaBox, err)
	}
	benBox, err := mail.ConnectAppPassword(ctx, ownerB, "", stalwartOrigin, benName, benPassword)
	if err != nil {
		t.Fatal(err)
	}

	// Reading: Ben's inbox has Carol's invoice and its thread.
	found, _, err := mail.Search(ctx, ownerB, benBox.ID, "INBOX", jmapmail.Query{Subject: "Invoice for October", Limit: 5})
	if err != nil || len(found) == 0 {
		t.Fatalf("search = %+v, %v", found, err)
	}
	thread, _, err := mail.Thread(ctx, ownerB, benBox.ID, found[0].ID)
	if err != nil || len(thread) < 2 {
		t.Fatalf("thread = %d, %v", len(thread), err)
	}
	if _, err := mail.Read(ctx, ownerA, benBox.ID, found[0].ID); !errors.Is(err, jmapaccounts.ErrNoAccount) {
		t.Fatalf("Ada read Ben's mailbox: %v", err)
	}

	// Drafting, confirming and sending from Ada to Ben, with a Bcc to Ada
	// herself that Ben must not see.
	identity, err := mail.Sender(ctx, ownerA, adaBox.ID)
	if err != nil {
		t.Fatal(err)
	}
	subject := "End-to-end " + time.Now().UTC().Format("150405.000")
	created, err := guard.Create(ctx, ownerA, ProviderJMAP, Message{AccountID: adaBox.ID, IdentityID: identity.ID,
		From: Address{Name: "Ada", Email: identity.Email}, To: []Address{{Name: "Ben", Email: benName}},
		Bcc: []Address{{Email: adaName}}, Subject: subject, Body: "Hello Ben,\nsent only after confirmation.\n"}, "")
	if err != nil {
		t.Fatal(err)
	}
	shown, err := guard.Confirm(ctx, ownerA, created.ID)
	if err != nil || shown.Confirmation == nil || shown.Draft.Bcc[0].Email != adaName {
		t.Fatalf("confirm = %+v, %v", shown, err)
	}
	// Ben, with Ada's token, gets nowhere.
	if _, err := guard.Send(ctx, ownerB, created.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, nil); !errors.Is(err, ErrNoDraft) {
		t.Fatalf("Ben sent Ada's draft: %v", err)
	}
	// Whatever the mailbox's history makes unusual (on a fresh server, Ben
	// is new to Ada) is ticked here; the unusual points are put to the
	// test on their own below.
	sent, err := guard.Send(ctx, ownerA, created.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, codes(shown.Confirmation.Unusual))
	if err != nil || sent.Draft.Status != StatusSent {
		t.Fatalf("send = %+v, %v", sent.Draft, err)
	}
	if _, err := guard.Send(ctx, ownerA, created.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, nil); !errors.Is(err, ErrConfirmationSpent) {
		t.Fatalf("a replay: %v", err)
	}

	// Ben has it, without the Bcc; Ada's copy is in her Sent folder and no
	// longer a draft.
	var received []jmapmail.Summary
	for attempt := 0; attempt < 20 && len(received) == 0; attempt++ {
		received, _, err = mail.Search(ctx, ownerB, benBox.ID, "INBOX", jmapmail.Query{Subject: subject, Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if len(received) == 0 {
			time.Sleep(250 * time.Millisecond)
		}
	}
	if len(received) != 1 || !strings.Contains(received[0].From, identity.Email) {
		t.Fatalf("Ben received %+v", received)
	}
	delivered, err := mail.Read(ctx, ownerB, benBox.ID, received[0].ID)
	if err != nil || len(delivered.Bcc) != 0 || strings.TrimSpace(delivered.Body) != "Hello Ben,\nsent only after confirmation." {
		t.Fatalf("delivered = %q with Bcc %v, %v", delivered.Body, delivered.Bcc, err)
	}
	// The hidden copy reached Ada's own inbox.
	hidden, _, err := mail.Search(ctx, ownerA, adaBox.ID, "INBOX", jmapmail.Query{Subject: subject, Limit: 5})
	if err != nil || len(hidden) != 1 {
		t.Fatalf("the Bcc copy = %+v, %v", hidden, err)
	}
	inSent, _, err := mail.Search(ctx, ownerA, adaBox.ID, "sent", jmapmail.Query{Subject: subject, Limit: 5})
	if err != nil || len(inSent) != 1 {
		t.Fatalf("Ada's Sent folder = %+v, %v", inSent, err)
	}
	inDrafts, _, err := mail.Search(ctx, ownerA, adaBox.ID, "drafts", jmapmail.Query{Subject: subject, Limit: 5})
	if err != nil || len(inDrafts) != 0 {
		t.Fatalf("the sent message is still in Drafts: %+v, %v", inDrafts, err)
	}
	var logged int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM logs WHERE user_id = $1 AND source = 'mail_send' AND status = 'SUCCESS'`, ownerA).Scan(&logged)
	if logged != 1 {
		t.Fatalf("%d sends logged", logged)
	}
	filesEndToEnd(t, ctx, guard, mail, studio, identity, adaBox.ID, benBox.ID, benName)

	// Disconnecting an app-password mailbox cannot revoke it from here.
	if revoked, err := mail.Disconnect(ctx, ownerA, adaBox.ID); err != nil || revoked {
		t.Fatalf("disconnect = %v, %v", revoked, err)
	}
}

// memoryFiles stands in for the file store a Studio output is read from
// (mailattachments): file 1 is a deck, whose bytes a test may change. A
// forwarded attachment is downloaded from the mailbox it is in, as
// mailattachments does.
type memoryFiles struct {
	files map[int64][]byte
	mail  *jmapaccounts.Mail
}

func (m *memoryFiles) Open(ctx context.Context, ownerID string, attachment Attachment) (io.ReadCloser, error) {
	if attachment.Source.Kind == SourceMail {
		data, err := m.mail.Download(ctx, ownerID, attachment.Source.AccountID,
			jmapmail.Attachment{BlobID: attachment.Source.BlobID, Name: attachment.Name, Type: attachment.Type}, attachment.Size)
		return io.NopCloser(bytes.NewReader(data)), err
	}
	return io.NopCloser(bytes.NewReader(m.files[attachment.Source.FileID])), nil
}

func (m *memoryFiles) Changed(context.Context, string, []Attachment) (string, error) { return "", nil }

func sha(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// filesEndToEnd sends Ada's deck and a statement forwarded from her inbox
// to Ben, with Carol, whom Ada never wrote to, in Cc; then shows that a
// deck changed after the draft was made is never sent.
func filesEndToEnd(t *testing.T, ctx context.Context, guard *Guard, mail *jmapaccounts.Mail, studio *memoryFiles,
	identity jmapmail.Identity, adaBox, benBox, benName string) {
	t.Helper()
	// The statement Carol sent Ada, a PDF, forwarded by its blob.
	statements, _, err := mail.Search(ctx, ownerA, adaBox, "INBOX", jmapmail.Query{Subject: "Your October statement", Limit: 5})
	if err != nil || len(statements) == 0 {
		t.Fatalf("Ada's statement = %+v, %v (the test mail m3.eml is imported into her inbox)", statements, err)
	}
	statement, err := mail.Read(ctx, ownerA, adaBox, statements[0].ID)
	if err != nil || len(statement.Attachments) != 1 {
		t.Fatalf("the statement = %+v, %v", statement, err)
	}
	pdf, err := mail.Download(ctx, ownerA, adaBox, statement.Attachments[0], int64(statement.Attachments[0].Size))
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("the statement's PDF = %d bytes, %v", len(pdf), err)
	}
	deck := studio.files[1]
	files := []Attachment{
		{Name: "launch.pptx", Type: "application/vnd.openxmlformats-officedocument.presentationml.presentation", Size: int64(len(deck)),
			SHA256: sha(deck), Origin: "Studio output: Launch", Source: AttachmentSource{Kind: SourceArtifact, FileID: 1}},
		{Name: "statement.pdf", Type: "application/pdf", Size: int64(len(pdf)), SHA256: sha(pdf), Origin: "Attachment of the statement",
			Source: AttachmentSource{Kind: SourceMail, AccountID: adaBox, MessageID: statement.ID, BlobID: statement.Attachments[0].BlobID}},
	}
	subject := "Files " + time.Now().UTC().Format("150405.000")
	// Carol by an address of this run's own (Stalwart delivers a
	// sub-address to her), so she is new to Ada however often this runs.
	carol := "carol+" + time.Now().UTC().Format("150405000") + "@example.test"
	message := Message{AccountID: adaBox, IdentityID: identity.ID, From: Address{Name: "Ada", Email: identity.Email},
		To: []Address{{Name: "Ben", Email: benName}}, Cc: []Address{{Email: carol}},
		Subject: subject, Body: "The deck and the statement.\n", Attachments: files}
	created, err := guard.Create(ctx, ownerA, ProviderJMAP, message, "")
	if err != nil {
		t.Fatal(err)
	}
	shown, err := guard.Confirm(ctx, ownerA, created.ID)
	if err != nil || shown.Confirmation == nil {
		t.Fatalf("confirm = %+v, %v", shown, err)
	}
	// Ada has written to Ben (above) and never to Carol: the real Sent
	// folder says so.
	if len(shown.Confirmation.Unusual) != 1 || shown.Confirmation.Unusual[0].Code != UnusualNewRecipients ||
		strings.Join(shown.Confirmation.Unusual[0].Addresses, ",") != carol {
		t.Fatalf("the unusual points = %+v", shown.Confirmation.Unusual)
	}
	if len(shown.Draft.Attachments) != 2 || shown.Draft.Attachments[1].Size != int64(len(pdf)) {
		t.Fatalf("the card's files = %+v", shown.Draft.Attachments)
	}
	if _, err := guard.Send(ctx, ownerA, created.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, nil); !errors.Is(err, ErrUnusualNotConfirmed) {
		t.Fatalf("an unticked new recipient: %v", err)
	}
	sent, err := guard.Send(ctx, ownerA, created.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, []string{UnusualNewRecipients})
	if err != nil || sent.Draft.Status != StatusSent {
		t.Fatalf("send = %+v, %v", sent.Draft, err)
	}
	// Ben has both files, byte for byte.
	var received []jmapmail.Summary
	for attempt := 0; attempt < 20 && len(received) == 0; attempt++ {
		received, _, err = mail.Search(ctx, ownerB, benBox, "INBOX", jmapmail.Query{Subject: subject, Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if len(received) == 0 {
			time.Sleep(250 * time.Millisecond)
		}
	}
	if len(received) != 1 {
		t.Fatalf("Ben received %+v", received)
	}
	delivered, err := mail.Read(ctx, ownerB, benBox, received[0].ID)
	if err != nil || len(delivered.Attachments) != 2 {
		t.Fatalf("delivered = %+v, %v", delivered.Attachments, err)
	}
	for index, want := range [][]byte{deck, pdf} {
		got, err := mail.Download(ctx, ownerB, benBox, delivered.Attachments[index], int64(delivered.Attachments[index].Size))
		if err != nil || !bytes.Equal(got, want) || delivered.Attachments[index].Name != files[index].Name {
			t.Fatalf("attachment %d = %q %d bytes, %v", index, delivered.Attachments[index].Name, len(got), err)
		}
	}

	// A deck changed after its draft was made is never sent: the upload
	// stops at its end, and no message is made.
	changedSubject := "Changed " + time.Now().UTC().Format("150405.000")
	message.Subject, message.Cc, message.Attachments = changedSubject, nil, files[:1]
	created, err = guard.Create(ctx, ownerA, ProviderJMAP, message, "")
	if err != nil {
		t.Fatal(err)
	}
	shown, err = guard.Confirm(ctx, ownerA, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	studio.files[1] = append([]byte("PK\x03\x04 another deck "), deck[24:]...)
	view, err := guard.Send(ctx, ownerA, created.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, codes(shown.Confirmation.Unusual))
	var failure *SendFailure
	if !errors.As(err, &failure) || !strings.Contains(failure.Message, "launch.pptx changed after this draft was made") || view.Draft.Status != StatusFailed {
		t.Fatalf("a changed deck = %+v, %v", view.Draft, err)
	}
	time.Sleep(time.Second)
	for _, folder := range []string{"sent", "drafts"} {
		if found, _, err := mail.Search(ctx, ownerA, adaBox, folder, jmapmail.Query{Subject: changedSubject, Limit: 5}); err != nil || len(found) != 0 {
			t.Fatalf("a message with the changed deck exists in %s: %+v, %v", folder, found, err)
		}
	}
	if found, _, err := mail.Search(ctx, ownerB, benBox, "INBOX", jmapmail.Query{Subject: changedSubject, Limit: 5}); err != nil || len(found) != 0 {
		t.Fatalf("Ben received the changed deck: %+v, %v", found, err)
	}
}
