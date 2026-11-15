package mailattachments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/maildrafts"
)

// The opt-in test below finds files over a real Postgres at the schema
// soloclef_schema's `migrate up` makes, and a local file store in a
// temporary directory. It empties the database it is given, so it must be
// a throwaway one:
//
//	SOLOCLEF_TEST_MAILATTACHMENTS_DATABASE=postgres://check:check@127.0.0.1:5432/check \
//	  go test ./mailattachments/ -count=1 -v
const databaseVariable = "SOLOCLEF_TEST_MAILATTACHMENTS_DATABASE"

const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
)

// The files the seed's rows name, by storage key.
var stored = map[string][]byte{
	"documents/ada/1/ORIGINAL/report.pdf": []byte("%PDF-1.7 the quarterly report"),
	"artifacts/ada/2/primary/deck.pptx":   []byte("PK the slides"),
	"documents/ben/3/ORIGINAL/secret.pdf": []byte("%PDF-1.7 Ben's own"),
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// mailbox is Ada's JMAP mailbox with one message carrying a PDF.
type mailbox struct{ downloads int }

func (m *mailbox) Read(_ context.Context, ownerID, accountID, messageID string) (jmapmail.Email, error) {
	if ownerID != ada || accountID != "box" || messageID != "M1" {
		return jmapmail.Email{}, &jmapmail.Failure{Message: "No message with this id is in the mailbox"}
	}
	return jmapmail.Email{ID: "M1", Subject: "Invoice\nfor October", From: []jmapmail.Address{{Name: "Carol", Email: "carol@example.net"}},
		Attachments: []jmapmail.Attachment{{BlobID: "B1", Name: "invoice.pdf", Type: "application/pdf", Size: 9}}}, nil
}

func (m *mailbox) Download(_ context.Context, _, _ string, attachment jmapmail.Attachment, most int64) ([]byte, error) {
	m.downloads++
	if attachment.BlobID != "B1" || most != 9 {
		return nil, errors.New("not the blob listed")
	}
	return []byte("%PDF-inv!"), nil
}

func open(t *testing.T) (*Files, *pgxpool.Pool, *mailbox) {
	t.Helper()
	address := os.Getenv(databaseVariable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway Postgres", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	directory := t.TempDir()
	for key, data := range stored {
		path := filepath.Join(directory, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	local, err := filestore.OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	stores, err := filestore.NewStores(local)
	if err != nil {
		t.Fatal(err)
	}
	// Document 1 is Ada's upload (its row recorded no checksum), 2 her
	// Studio deck, 4 her note (no file), 5 a second upload titled as 1
	// is; 3 is Ben's.
	_, err = pool.Exec(ctx, `
TRUNCATE "user" CASCADE;
INSERT INTO "user" (id, email) VALUES ('`+ada+`', 'ada@example.test'), ('`+ben+`', 'ben@example.test');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash) VALUES
  (1, '`+ada+`', 'Quarterly report', 'FILE', '', 'h1'),
  (2, '`+ada+`', 'Launch slides', 'ARTIFACT', '', 'h2'),
  (3, '`+ben+`', 'Secret', 'FILE', '', 'h3'),
  (4, '`+ada+`', 'Shopping list', 'NOTE', '', 'h4'),
  (5, '`+ada+`', 'Twice', 'FILE', '', 'h5'),
  (6, '`+ada+`', 'Twice', 'FILE', '', 'h6');
INSERT INTO document_files (id, document_id, user_id, storage_backend, storage_key, original_filename, mime_type, size_bytes) VALUES
  (11, 1, '`+ada+`', 'local', 'documents/ada/1/ORIGINAL/report.pdf', 'uploads/Q3 report.pdf', 'application/pdf', 29),
  (13, 3, '`+ben+`', 'local', 'documents/ben/3/ORIGINAL/secret.pdf', 'secret.pdf', 'application/pdf', 18),
  (15, 5, '`+ada+`', 'local', 'documents/ada/1/ORIGINAL/report.pdf', 'a.pdf', NULL, 29),
  (16, 6, '`+ada+`', 'local', 'documents/ada/1/ORIGINAL/report.pdf', 'b.pdf', NULL, 29);
INSERT INTO artifacts (id, document_id, user_id, format) VALUES (20, 2, '`+ada+`', 'slides');
INSERT INTO artifact_files (id, artifact_id, role, storage_backend, storage_key, original_filename, mime_type, size_bytes, checksum_sha256) VALUES
  (21, 20, 'primary', 'local', 'artifacts/ada/2/primary/deck.pptx', 'launch-slides.pptx',
   'application/vnd.openxmlformats-officedocument.presentationml.presentation', 13, '`+digestOf(stored["artifacts/ada/2/primary/deck.pptx"])+`');
`)
	if err != nil {
		t.Fatalf("seeding: %v", err)
	}
	mail := &mailbox{}
	files := &Files{Pool: pool, Stores: stores, Mail: mail}
	if err := files.Check(); err != nil {
		t.Fatal(err)
	}
	return files, pool, mail
}

func TestFilesAreFoundReadAndCheckedOverARealDatabase(t *testing.T) {
	files, pool, mail := open(t)
	ctx := context.Background()
	report := stored["documents/ada/1/ORIGINAL/report.pdf"]

	// An upload with no recorded checksum: its bytes are read for it, and
	// its name loses the path it was uploaded under.
	upload, err := files.Resolve(ctx, ada, Request{DocumentID: 1}, 0)
	if err != nil || upload.Name != "Q3 report.pdf" || upload.Size != 29 || upload.SHA256 != digestOf(report) ||
		upload.Origin != "Document: Quarterly report" || upload.Source != (maildrafts.AttachmentSource{Kind: maildrafts.SourceDocument, FileID: 11}) {
		t.Fatalf("the upload = %+v, %v", upload, err)
	}
	// A Studio output, by its document, its artifact and its title: the
	// deck's own file, with the digest its row recorded.
	for name, request := range map[string]Request{"document": {DocumentID: 2}, "artifact": {ArtifactID: 20}, "title": {Title: "launch SLIDES"}} {
		deck, err := files.Resolve(ctx, ada, request, 0)
		if err != nil || deck.Name != "launch-slides.pptx" || deck.Source.Kind != maildrafts.SourceArtifact || deck.Source.FileID != 21 ||
			deck.Origin != "Studio output: Launch slides" || deck.SHA256 != digestOf(stored["artifacts/ada/2/primary/deck.pptx"]) {
			t.Errorf("the deck by %s = %+v, %v", name, deck, err)
		}
	}
	// A forwarded attachment: downloaded once for its digest, no further
	// than the size the server listed.
	invoice, err := files.Resolve(ctx, ada, Request{AccountID: "box", MessageID: "M1", Filename: "INVOICE.pdf"}, 0)
	if err != nil || invoice.SHA256 != digestOf([]byte("%PDF-inv!")) || invoice.Source.BlobID != "B1" ||
		invoice.Origin != `Attachment of "Invoice for October" from Carol <carol@example.net>` || mail.downloads != 1 {
		t.Fatalf("the forwarded file = %+v, %v", invoice, err)
	}

	// What cannot be attached is said in words the model acts on.
	for name, c := range map[string]struct {
		owner   string
		request Request
		most    int64
		want    string
	}{
		"another account's document": {ada, Request{DocumentID: 3}, 0, "is not one of the user's documents that has a file"},
		"a note":                     {ada, Request{DocumentID: 4}, 0, "text only"},
		"two documents of one title": {ada, Request{Title: "Twice"}, 0, "document_id=6 \"Twice\""},
		"no such title":              {ada, Request{Title: "Nothing"}, 0, "no document of the user is titled"},
		"over the server's limit":    {ada, Request{DocumentID: 1}, 20, "accepts files of at most 1 KB (its upload limit)"},
		"two kinds at once":          {ada, Request{DocumentID: 1, Title: "x"}, 0, "exactly one of"},
		"no such attachment":         {ada, Request{AccountID: "box", MessageID: "M1", Filename: "other.pdf"}, 0, `its attachments are "invoice.pdf"`},
		"Ben names Ada's deck":       {ben, Request{ArtifactID: 20}, 0, "is not one of the user's Studio outputs"},
	} {
		_, err := files.Resolve(ctx, c.owner, c.request, c.most)
		var said *Refused
		if !errors.As(err, &said) || !strings.Contains(said.Message, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// At send time: the bytes come back from where they live, as the owner.
	stream, err := files.Open(ctx, ada, upload)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(stream)
	stream.Close()
	if string(got) != string(report) {
		t.Fatalf("opened %q", got)
	}
	if _, err := files.Open(ctx, ben, upload); err == nil {
		t.Fatal("Ben opened Ada's file")
	}

	// Before a go-ahead: unchanged rows pass; a removed or replaced file
	// is named.
	if changed, err := files.Changed(ctx, ada, []maildrafts.Attachment{upload, invoice}); err != nil || changed != "" {
		t.Fatalf("unchanged files = %q, %v", changed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE artifact_files SET checksum_sha256 = repeat('0', 64) WHERE id = 21`); err != nil {
		t.Fatal(err)
	}
	deck, _ := files.Resolve(ctx, ada, Request{DocumentID: 2}, 0)
	deck.SHA256 = digestOf(stored["artifacts/ada/2/primary/deck.pptx"])
	if changed, err := files.Changed(ctx, ada, []maildrafts.Attachment{upload, deck}); err != nil || changed != "launch-slides.pptx" {
		t.Fatalf("a replaced deck = %q, %v", changed, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM document_files WHERE id = 11`); err != nil {
		t.Fatal(err)
	}
	if changed, err := files.Changed(ctx, ada, []maildrafts.Attachment{upload}); err != nil || changed != "Q3 report.pdf" {
		t.Fatalf("a removed upload = %q, %v", changed, err)
	}
}
