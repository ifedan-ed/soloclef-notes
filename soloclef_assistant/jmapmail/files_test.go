package jmapmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// reportPDF is the file the fixture sends: a small PDF, as bytes.
var reportPDF = []byte("%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj 2 0 obj<</Type/Pages/Kids[]/Count 0>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n")

// opened counts how often a file was opened and closed.
type opened struct {
	opens, closes int
	closeErr      error
}

func (o *opened) file(data []byte) func(context.Context) (io.ReadCloser, error) {
	return func(context.Context) (io.ReadCloser, error) {
		o.opens++
		return closer{Reader: bytes.NewReader(data), close: func() error { o.closes++; return o.closeErr }}, nil
	}
}

type closer struct {
	io.Reader
	close func() error
}

func (c closer) Close() error { return c.close() }

func TestFixtureSendUploadsAFileAndAttachesIt(t *testing.T) {
	f := newFixtures(t, "send_files")
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
	files := &opened{}
	id, err := client.Send(context.Background(), session, credential, Outgoing{
		IdentityID: identities[0].ID, From: Address{Name: "Ada", Email: identities[0].Email},
		To: []Address{{Email: "ben@example.test"}}, Subject: "Fixture files", Body: "The report is attached.\n",
		Attachments: []OutgoingAttachment{{Name: "Q3 report.pdf", Type: "application/pdf", Size: int64(len(reportPDF)), Open: files.file(reportPDF)}},
	})
	if err != nil || id == "" {
		t.Fatalf("send = %q, %v", id, err)
	}
	if files.opens != 1 || files.closes != 1 {
		t.Fatalf("the file was opened %d and closed %d times", files.opens, files.closes)
	}
	// The file went to the upload endpoint, as itself, with its type and
	// exact length; the message then named the blob the server returned.
	var upload *sentRequest
	for index := range f.sent {
		if strings.HasPrefix(f.sent[index].Path, "/jmap/upload/") {
			upload = &f.sent[index]
		}
	}
	if upload == nil || upload.Method != "POST" || !bytes.Equal(upload.Body, reportPDF) {
		t.Fatalf("no upload of the file among %d requests", len(f.sent))
	}
	calls := f.calls(len(f.sent))
	var create map[string]any
	_ = json.Unmarshal(calls["e"][1], &create)
	email := create["create"].(map[string]any)["draft"].(map[string]any)
	attached, _ := email["attachments"].([]any)
	if len(attached) != 1 {
		t.Fatalf("the message names %d attachments: %s", len(attached), calls["e"][1])
	}
	part := attached[0].(map[string]any)
	if part["name"] != "Q3 report.pdf" || part["type"] != "application/pdf" || part["disposition"] != "attachment" || part["blobId"] == "" {
		t.Fatalf("the attachment = %v", part)
	}
}

func TestFixtureNeverMailedIsExactAboutWhoTheSentFolderReached(t *testing.T) {
	f := newFixtures(t, "never_mailed")
	client := fixtureClient(f)
	credential := f.credential("sender")
	session, err := client.Session(context.Background(), fixtureOrigin, credential)
	if err != nil {
		t.Fatal(err)
	}
	// Ben had the fixture's sends; "en@" is inside "ben@", which the
	// server's text filter matches and the exact check does not.
	never, err := client.NeverMailed(context.Background(), session, credential,
		[]string{"Ben@Example.test", "en@example.test", "nobody@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(never, ",") != "en@example.test,nobody@example.test" {
		t.Fatalf("never mailed = %v", never)
	}
}

func TestSendNamesNoBlobWhoseFileWasNotVouchedFor(t *testing.T) {
	changed := errors.New("not the confirmed bytes")
	server := &stub{replies: []exchangeFixture{
		{Status: 200, Body: json.RawMessage(strings.Replace(stubSession, `"downloadUrl"`, `"uploadUrl":"/jmap/upload/{accountId}/","downloadUrl"`, 1))},
		{Status: 200, Body: json.RawMessage(`{"methodResponses":[["Mailbox/get",{"list":[{"id":"d","name":"Drafts","role":"drafts"}]},"m"],["Identity/get",{"list":[{"id":"i1","email":"ada@example.test"}]},"i"]]}`)},
		{Status: 200, Body: json.RawMessage(`{"accountId":"A","blobId":"G1","type":"application/pdf","size":5}`)},
	}}
	client := Client{Network: server, Timeout: time.Second, AttachmentTimeout: time.Second}
	session, err := client.Session(context.Background(), "https://mail.example.test", Credential{AccessToken: "t"})
	if err != nil {
		t.Fatal(err)
	}
	files := &opened{closeErr: changed}
	_, err = client.Send(context.Background(), session, Credential{AccessToken: "t"}, Outgoing{
		IdentityID: "i1", From: Address{Email: "ada@example.test"}, To: []Address{{Email: "ben@example.test"}}, Subject: "s", Body: "b",
		Attachments: []OutgoingAttachment{{Name: "a.pdf", Type: "application/pdf", Size: 5, Open: files.file([]byte("%PDF!"))}},
	})
	// The upload went through, and its stream's owner then said the bytes
	// were not the ones meant: no message is made with that blob.
	if !errors.Is(err, changed) || len(server.sent) != 3 {
		t.Fatalf("send = %v after %d requests", err, len(server.sent))
	}
	// A file over the server's upload limit is not even opened.
	session.MaxSizeUpload = 4
	server.replies = []exchangeFixture{{Status: 200, Body: json.RawMessage(`{"methodResponses":[["Mailbox/get",{"list":[{"id":"d","name":"Drafts","role":"drafts"}]},"m"],["Identity/get",{"list":[{"id":"i1","email":"ada@example.test"}]},"i"]]}`)}}
	files = &opened{}
	_, err = client.Send(context.Background(), session, Credential{AccessToken: "t"}, Outgoing{
		IdentityID: "i1", From: Address{Email: "ada@example.test"}, To: []Address{{Email: "ben@example.test"}}, Subject: "s", Body: "b",
		Attachments: []OutgoingAttachment{{Name: "a.pdf", Type: "application/pdf", Size: 5, Open: files.file([]byte("%PDF!"))}},
	})
	var failure *Failure
	if !errors.As(err, &failure) || !strings.Contains(failure.Message, "upload limit") || files.opens != 0 {
		t.Fatalf("a file over the limit = %v, opened %d", err, files.opens)
	}
}
