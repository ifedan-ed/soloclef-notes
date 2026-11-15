package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapaccounts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailtools"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parserconfig"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/automationrun"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/workerconfig"

	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange/livechangetest"
)

// nothingPublished is a notification step's queue that takes every message.
type nothingPublished struct{}

func (nothingPublished) Send(context.Context, produce.Message) (string, error) { return "id", nil }

// A run is given the chat's mail tools as this tier builds them: an
// owner's Stalwart (JMAP) mailbox, connected and sealed by the API, is
// listed with the draft it can make, and its credential is opened with the
// worker's secret key, the API's own. What a conversation with the server
// then does is the API's code, tested against a real Stalwart in
// soloclef_assistant (maildrafts, mailtools); here the production network
// refuses the test's made-up server, which is the proof the sealed
// password was read and used.
func TestAnAutomationRunReadsAndDraftsStalwartMail(t *testing.T) {
	pool := testdatabase.Open(t)
	owner := testdatabase.Account(t, pool)
	ctx := context.Background()
	const secret = "the deployment's secret, shared by the API and the worker"

	// The mailbox as the API's connect stores it: the app password sealed
	// under the deployment's secret, bound to its owner.
	seal, err := jmapaccounts.NewSeal(secret)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := seal.Close(owner, jmapaccounts.Secrets{Password: "an app password"})
	if err != nil {
		t.Fatal(err)
	}
	const box = "5a5a5a5a-0000-4000-8000-00000000000a"
	if _, err := pool.Exec(ctx, `INSERT INTO jmap_mail_accounts (id, user_id, label, server_url, username, auth, credentials, can_send)
		VALUES ($1, $2::uuid, 'Work mail', 'https://mail.soloclef-test.invalid', 'ada@soloclef-test.invalid', 'app_password', $3, true)`,
		box, owner, sealed); err != nil {
		t.Fatal(err)
	}

	// The parser block every tier reads, as a deployment names it.
	for key, value := range map[string]string{
		parserconfig.DefaultParser: "local", parserconfig.LocalURL: "http://127.0.0.1:1", parserconfig.LocalTimeoutSeconds: "5",
		parserconfig.MaxTextBytes: "1048576", parserconfig.MaxMarkdownBytes: "1048576",
	} {
		t.Setenv(key, value)
	}
	cfg := automationrun.Config{
		Enabled: true, AppRedisURL: "redis://127.0.0.1:1/0", EmbeddingModel: "embedding", EmbeddingDimension: 1024,
		QdrantCollection: "chunks", QdrantHNSWEf: 64, NCBITool: "soloclef-test", Brand: "Soloclef", MaxSteps: 5, SecretKey: secret,
	}
	settings := workerconfig.Config{ProxyURL: "http://127.0.0.1:1", ProxyKey: "k", QdrantURL: "http://127.0.0.1:1", SecretKey: secret}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	own, err := ownmodels.ForDeployment(pool, cfg.SecretKey, time.Second, model.RetryPolicy{Attempts: 1, Backoff: time.Millisecond, MaxBackoff: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	files, err := filestore.OpenStores(filestore.Settings{Backend: filestore.LocalBackend, LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	notifier := &notify.Notifier{Database: pool, Publisher: nothingPublished{}, Changes: &livechangetest.Recorder{}, Log: log}
	tools, closeTools, err := automationTools(settings, cfg, pool, files, &http.Client{}, own, notifier, log)
	if err != nil {
		t.Fatalf("the run's tools: %v", err)
	}
	defer closeTools()

	connected, err := tools.Mail.Connected(ctx, owner)
	if err != nil || !connected {
		t.Fatalf("the Stalwart mailbox does not give a run mail tools: %v, %v", connected, err)
	}
	offered := map[string]tool.Tool{}
	for _, made := range tools.Mail.ForTurn(owner) {
		offered[made.Name] = made
	}
	for _, name := range []string{mailtools.SearchTool, mailtools.ReadTool, mailtools.ThreadTool, mailtools.DraftTool} {
		if _, ok := offered[name]; !ok {
			t.Fatalf("a run is not offered %s", name)
		}
	}
	listed, err := offered[mailtools.ListAccountsTool].Run(ctx, tool.Call{Name: mailtools.ListAccountsTool, Args: json.RawMessage(`{}`)})
	if err != nil || !strings.Contains(listed, `account_id="`+box+`" Work mail`) || !strings.Contains(listed, "draft_email can prepare") {
		t.Fatalf("listed = %q, %v", listed, err)
	}
	// The sealed password opens with the worker's key: the search gets as
	// far as the network, which reaches public servers only.
	found, err := offered[mailtools.SearchTool].Run(ctx, tool.Call{Name: mailtools.SearchTool, Args: json.RawMessage(`{"account_id":"` + box + `","subject":"x"}`)})
	if err != nil || !strings.Contains(found, "Not searched") || !strings.Contains(found, "Could not connect to this mail server") ||
		strings.Contains(found, "credentials") {
		t.Fatalf("a search of the mailbox from a run = %q, %v", found, err)
	}
	var reconnect bool
	if err := pool.QueryRow(ctx, `SELECT needs_reconnect FROM jmap_mail_accounts WHERE id = $1`, box).Scan(&reconnect); err != nil || reconnect {
		t.Fatalf("the mailbox was marked to reconnect (its credential was not read): %v, %v", reconnect, err)
	}
}
