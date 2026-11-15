package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

func init() {
	registerArea(automationsArea)
	registerArea(connectorsArea)
}

// Automations, as the builds' Automations screens use them
// (features/automations/api.ts): the list, one automation, its runs and one
// run, Cancel, the schedule triggers, model eligibility, and the builder's
// create, edit and delete. Ids 5500-5999.
var automationsArea = contractArea{
	Name:  "automations",
	Order: 55,
	Seed: `
INSERT INTO automations (id, user_id, name, definition) VALUES
 (5501, '` + testpostgres.Alice + `', 'Morning digest', '{"name":"Morning digest","plan":[{"step_id":"step_1","action":"agent_task","params":{"query":"Summarise what arrived."}}]}');
INSERT INTO automation_triggers (id, automation_id, type, params) VALUES
 (5601, 5501, 'schedule', '{"cron":"0 9 * * *","timezone":"UTC"}'),
 (5602, 5501, 'schedule', '{"cron":"0 18 * * *","timezone":"UTC"}');
INSERT INTO automation_runs (id, automation_id, trigger_id, status, definition_snapshot, started_at, created_at) VALUES
 (5701, 5501, 5601, 'running', '{"name":"Morning digest","plan":[{"step_id":"step_1","action":"agent_task","params":{"query":"Summarise what arrived."}}]}', '2026-11-12T09:00:01Z', '2026-11-12T09:00:00Z'),
 (5702, 5501, 5601, 'succeeded', '{"name":"Morning digest","plan":[{"step_id":"step_1","action":"agent_task","params":{"query":"Summarise what arrived."}}]}', '2026-11-11T09:00:01Z', '2026-11-11T09:00:00Z');
UPDATE automation_runs SET finished_at = '2026-11-11T09:01:00Z', output = '{"text":"Two new sources."}' WHERE id = 5702;`,
	Cases: []contractCase{
		// The pages ask for the server's largest page (AUTOMATIONS_PAGE, RUNS_PAGE).
		{Operation: "GET /api/v1/automations", Path: "/api/v1/automations?limit=200&offset=0", Status: 200},
		{Operation: "GET /api/v1/automations/model-eligibility", Path: "/api/v1/automations/model-eligibility", Status: 200},
		{Operation: "GET /api/v1/automations/{automation_id}", Path: "/api/v1/automations/5501", Status: 200},
		{Operation: "GET /api/v1/automations/{automation_id}/runs", Path: "/api/v1/automations/5501/runs?limit=200&offset=0", Status: 200},
		{Operation: "GET /api/v1/automations/{automation_id}/runs/{run_id}", Path: "/api/v1/automations/5501/runs/5702", Status: 200},
		{Operation: "POST /api/v1/automations/{automation_id}/runs/{run_id}/cancel", Path: "/api/v1/automations/5501/runs/5701/cancel", Status: 200},

		// The triggers screen: add a schedule, change its time, remove one
		// (TriggersScreen.tsx).
		{Operation: "POST /api/v1/automations/{automation_id}/triggers", Path: "/api/v1/automations/5501/triggers", Status: 201,
			Body: `{"type":"schedule","params":{"cron":"30 7 * * 1","timezone":"Africa/Lagos"},"static_inputs":{},"enabled":true}`},
		{Operation: "PATCH /api/v1/automations/{automation_id}/triggers/{trigger_id}", Path: "/api/v1/automations/5501/triggers/5601", Status: 200,
			Body: `{"params":{"cron":"0 8 * * *","timezone":"Africa/Lagos"}}`},
		{Operation: "DELETE /api/v1/automations/{automation_id}/triggers/{trigger_id}", Path: "/api/v1/automations/5501/triggers/5602", Status: 204},

		// The builder's create (lib/builderSchema.ts buildCreatePayload, with
		// the form's defaults), an edit, a pause, and the delete.
		{Operation: "POST /api/v1/automations", Path: "/api/v1/automations", Status: 201, Capture: map[string]string{"automation": "id"},
			Body: `{"name":"Weekly review","description":null,"definition":{"schema_version":"1.0","name":"Weekly review","goal":null,"triggers":[],` +
				`"plan":[{"step_id":"step_1","action":"agent_task","params":{"query":"What did I add this week?","auto_approve_all":false}}],` +
				`"execution":{"timeout_seconds":600,"max_retries":0,"retry_backoff":"exponential","concurrency":"drop_if_running","on_failure":[],"email_result":false},` +
				`"metadata":{"tags":[]}},"triggers":[{"type":"schedule","params":{"cron":"0 17 * * 5","timezone":"Africa/Lagos"},"static_inputs":{},"enabled":true}]}`},
		{Operation: "PATCH /api/v1/automations/{automation_id}", Path: "/api/v1/automations/{{automation}}", Status: 200,
			Body: `{"name":"Weekly review","description":"Fridays","definition":{"schema_version":"1.0","name":"Weekly review","goal":null,"triggers":[],` +
				`"plan":[{"step_id":"step_1","action":"agent_task","params":{"query":"What did I add this week, by notebook?","auto_approve_all":false}}],` +
				`"execution":{"timeout_seconds":600,"max_retries":0,"retry_backoff":"exponential","concurrency":"drop_if_running","on_failure":[],"email_result":false},` +
				`"metadata":{"tags":[]}}}`},
		{Operation: "PATCH /api/v1/automations/{automation_id}", Path: "/api/v1/automations/{{automation}}", Status: 200, Body: `{"status":"paused"}`},
		{Operation: "DELETE /api/v1/automations/{automation_id}", Path: "/api/v1/automations/{{automation}}", Status: 204},
	},
}

// Connectors, as the builds' Settings → Connectors and the add-source
// pages use them (features/settings/api.ts, features/sources/api.ts): the
// connector list and its switches, MCP servers, IMAP and Microsoft mail,
// Nextcloud and Google Drive. Ids 6000-6999.
//
// Google, Microsoft, Nextcloud, IMAP servers and MCP servers are not run
// here; the deployment names no Google or Microsoft client, and Nextcloud is
// an address nothing listens on (contractSettings). A case that reaches one
// of them says so (Unavailable) or expects the answer a build gets from a
// deployment without it.
var connectorsArea = contractArea{
	Name:  "connectors",
	Order: 56,
	Seed:  connectorsSeed(),
	Cases: []contractCase{
		// The connector list: Settings asks for a page of 200, the + menu and
		// the automation builder for the default.
		{Operation: "GET /api/v1/search-source-connectors", Path: "/api/v1/search-source-connectors?limit=200", Status: 200},
		{Operation: "GET /api/v1/search-source-connectors", Path: "/api/v1/search-source-connectors", Status: 200},
		// A connector's settings (ConnectorSettings.tsx) and its switch.
		{Operation: "PUT /api/v1/search-source-connectors/{connector_id}", Path: "/api/v1/search-source-connectors/6001", Status: 200,
			Body: `{"periodic_indexing_enabled":true,"indexing_frequency_minutes":1440}`},
		{Operation: "PUT /api/v1/search-source-connectors/{connector_id}/enabled", Path: "/api/v1/search-source-connectors/6001/enabled", Status: 200,
			Body: `{"enabled":false}`},
		{Operation: "PUT /api/v1/search-source-connectors/{connector_id}/enabled", Path: "/api/v1/search-source-connectors/6001/enabled", Status: 200,
			Body: `{"enabled":true}`},
		// "Sync now" on Drive asks Google; this deployment names no Google
		// client, which the route says before queueing anything.
		{Operation: "POST /api/v1/search-source-connectors/{connector_id}/index", Path: "/api/v1/search-source-connectors/6001/index",
			Status: 503, Unavailable: "Google (no OAuth client in this deployment)",
			Why: "a deployment without Google answers 503 google_drive_not_configured, which the build shows as its sentence"},

		// MCP servers (McpServerSheet.tsx, McpServersScreen.tsx).
		{Operation: "GET /api/v1/connectors/mcp", Path: "/api/v1/connectors/mcp", Status: 200},
		{Operation: "GET /api/v1/connectors/mcp/policy", Path: "/api/v1/connectors/mcp/policy", Status: 200},
		{Operation: "GET /api/v1/connectors/mcp/{connector_id}", Path: "/api/v1/connectors/mcp/6003", Status: 200},
		// Offered only for a trusted tool, and no server has any since
		// migration 15 (installedbuilds.Retired).
		{Operation: "POST /api/v1/connectors/mcp/{connector_id}/untrust-tool", Path: "/api/v1/connectors/mcp/6003/untrust-tool", Status: 404,
			Body: `{"tool_name":"search"}`, Why: "retired: installedbuilds.Retired says what the build does with it"},
		// Test reaches the MCP server; nothing listens there in this test.
		// The bundles before the fixes send no `auth`, which the server
		// refuses 422: their exceptions.json names the update that fixes it.
		{Operation: "POST /api/v1/connectors/mcp/test", Path: "/api/v1/connectors/mcp/test", Status: 200, Bundles: bundlesBeforeTheFixes,
			Body: `{"url":"https://tools.example.test/mcp","headers":{},"transport":"streamable-http","connector_id":6003}`},
		// The sheet's save: the builds' server_config is {url, headers,
		// transport}, the key in a header (MCPServerConfig in their snapshot).
		{Operation: "POST /api/v1/connectors/mcp", Path: "/api/v1/connectors/mcp", Status: 201, Bundles: bundlesBeforeTheFixes,
			Body: `{"name":"Notes tools","server_config":{"url":"https://notes-tools.example.test/mcp","headers":{"Authorization":"Bearer not-a-real-key"},"transport":"streamable-http"}}`},
		{Operation: "PUT /api/v1/connectors/mcp/{connector_id}", Path: "/api/v1/connectors/mcp/6003", Status: 200, Bundles: bundlesBeforeTheFixes,
			Body: `{"name":"Some tools","server_config":{"url":"https://tools.example.test/mcp","headers":{},"transport":"streamable-http"}}`},
		// The updates with the fixes (mcpServerSettings.ts): a bearer header
		// becomes the bearer sign-in, a saved server keeps its own (a mask
		// keeps the stored secret), anything else is none with its headers.
		{Operation: "POST /api/v1/connectors/mcp/test", Path: "/api/v1/connectors/mcp/test", Status: 200, Bundles: bundlesWithTheFixes,
			Body: `{"url":"https://tools.example.test/mcp","headers":{},"transport":"streamable-http","auth":{"type":"none"},"connector_id":6003}`},
		// A save is held to where the server may connect, which needs the
		// internet (the name must resolve to public addresses), and this
		// test has none: the destination's 400 is what a save gets here. It
		// comes after the settings were read, so it is the proof the fixed
		// body is accepted where the old one is the 422 above.
		{Operation: "POST /api/v1/connectors/mcp", Path: "/api/v1/connectors/mcp", Status: 400, Bundles: bundlesWithTheFixes,
			Body: `{"name":"Notes tools 2","server_config":{"url":"https://notes-tools-2.example.test/mcp","headers":{},"transport":"streamable-http","auth":{"type":"bearer","token":"not-a-real-key"}}}`,
			Why:  "the settings are accepted; the destination check needs the internet, which this test does not have"},
		{Operation: "PUT /api/v1/connectors/mcp/{connector_id}", Path: "/api/v1/connectors/mcp/6003", Status: 400, Bundles: bundlesWithTheFixes,
			Body: `{"name":"Some tools","server_config":{"url":"https://tools.example.test/mcp","headers":{"X-Org":"contract"},"transport":"streamable-http","auth":{"type":"none"}}}`,
			Why:  "the settings are accepted; the destination check needs the internet, which this test does not have"},
		{Operation: "DELETE /api/v1/connectors/mcp/{connector_id}", Path: "/api/v1/connectors/mcp/6003", Status: 204},

		// Mail: the IMAP accounts and Microsoft's sign-in (EmailConnectorScreen.tsx,
		// EmailAccountSheet.tsx).
		{Operation: "GET /api/v1/connectors/email-imap/accounts", Path: "/api/v1/connectors/email-imap/accounts", Status: 200},
		{Operation: "GET /api/v1/connectors/email-imap/microsoft", Path: "/api/v1/connectors/email-imap/microsoft", Status: 200},
		{Operation: "POST /api/v1/connectors/email-imap/test", Path: "/api/v1/connectors/email-imap/test", Status: 200,
			Body: `{"label":"Home","host":"imap.example.test","port":993,"use_tls":true,"username":"ada@example.test","app_password":"not-a-real-password"}`},
		{Operation: "POST /api/v1/connectors/email-imap/accounts/{account_id}/test", Path: "/api/v1/connectors/email-imap/accounts/acct-6004/test", Status: 200},
		// Adding a mailbox checks that its host resolves to public addresses
		// before anything is stored, and this test has no network: the build
		// shows the one sentence of the 400 under the form. The add itself
		// is held by internal/mailaccounts' own tests.
		{Operation: "POST /api/v1/connectors/email-imap/accounts", Path: "/api/v1/connectors/email-imap/accounts", Status: 400,
			Why:  "the host must resolve to a public address and this test has no network; the add sheet shows the refusal's sentence",
			Body: `{"label":"Home","host":"imap.example.test","port":993,"use_tls":true,"username":"ada@example.test","app_password":"not-a-real-password"}`},
		{Operation: "DELETE /api/v1/connectors/email-imap/accounts/{account_id}", Path: "/api/v1/connectors/email-imap/accounts/acct-6004", Status: 204},
		{Operation: "POST /api/v1/connectors/email-imap/microsoft/connect", Path: "/api/v1/connectors/email-imap/microsoft/connect",
			Status: 503, Unavailable: "Microsoft (no client in this deployment)",
			Why:  "a deployment without Microsoft answers 503 microsoft_mail_not_configured, which the build shows as its sentence",
			Body: `{"return_path":"/settings/connectors/email","account_id":null}`},

		// Nextcloud: the connection, Login Flow v2, and the add-source
		// picker (NextcloudConnectorScreen.tsx, NextcloudPage.tsx).
		{Operation: "GET /api/v1/nextcloud/connection", Path: "/api/v1/nextcloud/connection", Status: 200},
		{Operation: "GET /api/v1/nextcloud/refreshable-documents", Path: "/api/v1/nextcloud/refreshable-documents", Status: 200},
		{Operation: "GET /api/v1/connectors/{connector_id}/nextcloud/browse", Path: "/api/v1/connectors/6002/nextcloud/browse?path=%2F", Status: 502, Unavailable: "Nextcloud", Why: nextcloudAway},
		{Operation: "GET /api/v1/connectors/{connector_id}/nextcloud/search", Path: "/api/v1/connectors/6002/nextcloud/search?q=lecture", Status: 502, Unavailable: "Nextcloud", Why: nextcloudAway},
		{Operation: "POST /api/v1/connectors/{connector_id}/nextcloud/import/preview", Path: "/api/v1/connectors/6002/nextcloud/import/preview", Status: 502, Unavailable: "Nextcloud", Why: nextcloudAway,
			Body: `{"items":[{"path":"/Lectures/Lecture 5.pdf","kind":"file"}],"parser":"local","use_vision_llm":false}`},
		{Operation: "POST /api/v1/connectors/{connector_id}/nextcloud/import", Path: "/api/v1/connectors/6002/nextcloud/import", Status: 502, Unavailable: "Nextcloud", Why: nextcloudAway,
			Body: `{"items":[{"path":"/Lectures/Lecture 5.pdf","kind":"file"}],"parser":"local","use_vision_llm":false,"notebook_id":null}`},
		{Operation: "POST /api/v1/connectors/{connector_id}/nextcloud/documents/{document_id}/refresh", Path: "/api/v1/connectors/6002/nextcloud/documents/6101/refresh", Status: 502, Unavailable: "Nextcloud", Why: nextcloudAway},
		{Operation: "POST /api/v1/nextcloud/login", Path: "/api/v1/nextcloud/login", Status: 502, Unavailable: "Nextcloud", Why: nextcloudAway, Body: `{}`},
		// Without Nextcloud no sign-in can start, so the poll is of a flow
		// the server does not hold: the 404 the app reads as "This Nextcloud
		// sign-in expired; start again" (NextcloudConnectorScreen.tsx).
		{Operation: "POST /api/v1/nextcloud/login/{flow_id}/poll", Path: "/api/v1/nextcloud/login/flow-6201/poll", Status: 404,
			Why: "no sign-in can start without Nextcloud; an unknown flow is the expired-sign-in 404 the app shows as start again"},
		{Operation: "DELETE /api/v1/nextcloud/login/{flow_id}", Path: "/api/v1/nextcloud/login/flow-6201", Status: 204},
		{Operation: "DELETE /api/v1/connectors/{connector_id}/nextcloud/connection", Path: "/api/v1/connectors/6002/nextcloud/connection", Status: 200},

		// Google Drive (DriveConnectorScreen.tsx, DrivePage.tsx).
		{Operation: "GET /api/v1/google-drive/connection", Path: "/api/v1/google-drive/connection", Status: 200},
		{Operation: "POST /api/v1/google-drive/connect", Path: "/api/v1/google-drive/connect", Status: 503, Unavailable: "Google (no OAuth client in this deployment)",
			Why:  "a deployment without Google answers 503 google_drive_not_configured, which the build shows as its sentence",
			Body: `{"return_path":"/settings/connectors/google-drive"}`},
		{Operation: "DELETE /api/v1/connectors/{connector_id}/google-drive/connection", Path: "/api/v1/connectors/6001/google-drive/connection", Status: 200},
	},
}

// nextcloudAway is why a Nextcloud case expects a 502: Nextcloud is an
// address nothing listens on here, and the build shows the 502's "could not
// be reached" sentence and lets the person try again.
const nextcloudAway = "Nextcloud is not run here; the 502 is the could-not-reach answer the build shows"

// contractSealKey is the contract deployment's session secret: every
// credential a connection stores is sealed under it, and a row whose
// credential the server cannot open is not a row any route writes (an
// unsealed mailbox makes the whole connector list a 500, by design:
// internal/searchconnectors validated).
const contractSealKey = contractSessionSecret

// connectorsSeed is the connections as the server itself stores them after
// a person connected each: Drive, Nextcloud (Login Flow v2's stored form),
// an MCP server with no sign-in, and one IMAP mailbox, each credential
// sealed. Bob's connections are not seeded: isolation is each package's own
// database test.
func connectorsSeed() string {
	now := time.Now().UTC()
	seal := func(plain string) string {
		sealed, err := storedtoken.Seal(contractSealKey, plain, now, rand.Reader)
		if err != nil {
			panic(err)
		}
		return sealed
	}
	encode := func(value any) string {
		raw, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		return string(raw)
	}
	serverSeal, err := sealedsecret.New(contractSealKey, sealedsecret.MCPServerSecret)
	if err != nil {
		panic(err)
	}
	server, err := mcpserver.Build(serverSeal, testpostgres.Alice, mcpserver.Settings{
		Address: "https://tools.example.test/mcp", Transport: mcpserver.StreamableHTTP, Auth: mcpserver.Auth{Kind: mcpserver.AuthNone}}, nil)
	if err != nil {
		panic(err)
	}
	nextcloud := encode(map[string]any{"server_url": "http://127.0.0.1:9", "username": "ada", "login_name": "ada", "display_name": "Ada",
		"auth_method": "login_flow", "app_password": seal("not-a-real-app-password"), "_token_encrypted": true})
	mcp := encode(map[string]any{"server_config": server, "trusted_tools": []string{"search"}})
	mail := encode(map[string]any{"accounts": []any{map[string]any{"id": "acct-6004", "label": "Work", "host": "imap.example.test", "port": 993,
		"use_tls": true, "username": "ada@example.test", "app_password": seal("not-a-real-password"), "_encrypted": true, "created_at": "2026-11-01T08:00:00Z"}}})
	return fmt.Sprintf(`
INSERT INTO search_source_connectors (id, user_id, name, connector_type, is_indexable, config) VALUES
 (6001, '%[1]s', 'Google Drive', 'GOOGLE_DRIVE_CONNECTOR', true, '{"selected_folders":[],"selected_files":[]}'),
 (6002, '%[1]s', 'Nextcloud', 'NEXTCLOUD_CONNECTOR', false, '%[2]s'),
 (6003, '%[1]s', 'Some tools', 'MCP_CONNECTOR', false, '%[3]s'),
 (6004, '%[1]s', 'Mail', 'EMAIL_IMAP_CONNECTOR', false, '%[4]s');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, connector_id, document_metadata, created_at) VALUES
 (6101, '%[1]s', 'Lecture 4.pdf', 'FILE', 'Tides.', 'contract-6101', 6002,
  '{"nextcloud":{"server_url":"http://127.0.0.1:9","username":"ada","path":"/Lectures/Lecture 4.pdf","etag":"e1"}}', '2026-11-01T08:00:00Z');`,
		testpostgres.Alice, nextcloud, mcp, mail)
}
