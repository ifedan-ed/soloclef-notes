package mailtools

import (
	"context"
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
	"git.danvics.com/danvics/soloclef/soloclef_ingest/fileread"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parserconfig"
)

// The end-to-end test below reads a PDF attached to a message in a real
// Stalwart mailbox with the real parse service (pymupdf4llm), the way a
// chat turn does: read_email with attachment_filename. It runs only when it
// is given a throwaway Stalwart whose public address is
// https://mail.example.test with Ada's mailbox holding the statement
// (Carol's message with statement.pdf), a parse service, and a throwaway
// database:
//
//	JMAP_E2E_STALWART=http://127.0.0.1:PORT JMAP_E2E_SENDER='ada@example.test:password' \
//	JMAP_E2E_PARSE_SERVICE=http://127.0.0.1:PORT \
//	SOLOCLEF_API_TEST_MAILTOOLS_DATABASE=postgres://... \
//	  go test ./mailtools/ -run EndToEnd -count=1 -v

// loopback reaches the test's Stalwart for https://mail.example.test.
type loopback struct{ target *url.URL }

func (l loopback) Client(string) (*http.Client, error) {
	return &http.Client{
		Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
			if request.URL.Host != "mail.example.test" {
				return nil, errors.New("the test's network reaches mail.example.test only")
			}
			forwarded := request.Clone(request.Context())
			forwarded.URL.Scheme, forwarded.URL.Host, forwarded.Host = l.target.Scheme, l.target.Host, l.target.Host
			return http.DefaultTransport.RoundTrip(forwarded)
		}),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

type roundTrip func(*http.Request) (*http.Response, error)

func (r roundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return r(request) }

// parsed reads files with the deployment's own reader, as the account's
// reader (accountfiles) does for a document.
type parsed struct{ reader *fileread.Reader }

func (p parsed) Read(ctx context.Context, _, parser string, file fileread.File) (fileread.Text, error) {
	return p.reader.Read(ctx, file, fileread.Choices{Parser: parser})
}

func TestEndToEndAPDFAttachedInStalwartIsReadByTheParseService(t *testing.T) {
	server, sender, parseService := os.Getenv("JMAP_E2E_STALWART"), os.Getenv("JMAP_E2E_SENDER"), os.Getenv("JMAP_E2E_PARSE_SERVICE")
	database := os.Getenv(databaseVariable)
	if server == "" || sender == "" || parseService == "" || database == "" {
		t.Skip("JMAP_E2E_STALWART, JMAP_E2E_SENDER, JMAP_E2E_PARSE_SERVICE and the database are needed")
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
	if _, err := pool.Exec(ctx, `TRUNCATE "user" CASCADE; INSERT INTO "user" (id, email) VALUES ('`+ada+`', 'ada@example.test')`); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	seal, err := jmapaccounts.NewSeal("an end-to-end test secret")
	if err != nil {
		t.Fatal(err)
	}
	network := loopback{target: target}
	mail := &jmapaccounts.Mail{
		SignIn: jmapaccounts.SignIn{Rows: jmapaccounts.Store{Pool: pool}, Seal: seal,
			OAuth: jmapaccounts.OAuth{Network: network, Timeout: 10 * time.Second}, Now: time.Now, Logger: logger},
		Client: jmapmail.Client{Network: network, Timeout: 20 * time.Second, AttachmentTimeout: 30 * time.Second},
	}
	name, password, _ := strings.Cut(sender, ":")
	box, err := mail.ConnectAppPassword(ctx, ada, "Ada at Stalwart", "https://mail.example.test", name, password)
	if err != nil {
		t.Fatal(err)
	}
	// The parser block a deployment gives every tier, pointed at the parse
	// service, with pymupdf4llm ("local") as the default.
	settings := map[string]string{
		parserconfig.DefaultParser: "local", parserconfig.LocalURL: parseService, parserconfig.LocalTimeoutSeconds: "60",
		parserconfig.MaxTextBytes: "10485760", parserconfig.MaxMarkdownBytes: "10485760",
	}
	parsers, err := parserconfig.Load(func(key string) string { return settings[key] })
	if err != nil {
		t.Fatal(err)
	}
	reader, err := fileread.New(parsers, logger)
	if err != nil {
		t.Fatal(err)
	}
	tools, _, _ := stocked()
	tools.Rows, tools.JMAP, tools.Files = Store{Pool: pool}, mail, parsed{reader: reader}
	if err := tools.Check(); err != nil {
		t.Fatal(err)
	}

	found, err := run(t, tools, ada, SearchTool, `{"account_id":"`+box.ID+`","subject":"Your October statement"}`)
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(found, `message_id="`)
	if !ok {
		t.Fatalf("the statement was not found: %s", found)
	}
	messageID, _, _ := strings.Cut(after, `"`)
	read, err := run(t, tools, ada, ReadTool, `{"account_id":"`+box.ID+`","message_id":"`+messageID+`","attachment_filename":"statement.pdf"}`)
	if err != nil {
		t.Fatal(err)
	}
	// The PDF's own text, as the parse service read it, is in the answer;
	// nothing is stored (the documents table stays empty).
	if !strings.Contains(read, `Content of the attachment "statement.pdf":`) || !strings.Contains(read, "Statement for October: balance 310 EUR") {
		t.Fatalf("read = %s", read)
	}
	var documents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM documents`).Scan(&documents); err != nil || documents != 0 {
		t.Fatalf("%d documents after reading an attachment, %v", documents, err)
	}
}
