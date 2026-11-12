import assert from "node:assert/strict";
import { mock, test } from "node:test";
import {
	EMAIL_MAILBOXES_NOTE,
	editViewActions,
} from "@/components/assistant-ui/connector-popup/connector-configs/views/edit-view-actions";
import { baseApiService } from "@/lib/apis/base-api.service";
import { connectorsApiService } from "@/lib/apis/connectors-api.service";

const noPanel = { hasSettingsPanel: false, hasVisionToggle: false };
const withPanel = { hasSettingsPanel: true, hasVisionToggle: false };

const email = {
	connector_type: "EMAIL_IMAP_CONNECTOR",
	is_indexable: false,
	config: { accounts: [{ id: "a" }] },
} as const;

test("your mail's settings have the switch only: no Save, no Disconnect, a pointer to the card", () => {
	const actions = editViewActions(email, noPanel);
	assert.equal(actions.canSave, false);
	assert.equal(actions.canDisconnect, false);
	assert.equal(actions.mailboxesNote, EMAIL_MAILBOXES_NOTE);
	assert.match(actions.mailboxesNote ?? "", /Email \(IMAP\) card/);
	assert.doesNotMatch(actions.subtitle, /sync configuration/);
	assert.doesNotMatch(`${actions.subtitle} ${actions.mailboxesNote}`, /workspace/i);
});

test("every listed connection is the account's own, so none opens read-only", () => {
	for (const type of ["GOOGLE_DRIVE_CONNECTOR", "NEXTCLOUD_CONNECTOR"] as const) {
		const actions = editViewActions(
			{ connector_type: type, is_indexable: true, config: {} },
			withPanel
		);
		assert.equal(Object.hasOwn(actions, "readOnly"), false, type);
		assert.equal(actions.canSave, true, type);
		assert.equal(actions.canDisconnect, true, type);
		assert.equal(actions.mailboxesNote, null, type);
		assert.doesNotMatch(actions.subtitle, /Another member/, type);
	}
});

test("Disconnect says what is lost, and names no workspace", () => {
	const drive = editViewActions(
		{ connector_type: "GOOGLE_DRIVE_CONNECTOR", is_indexable: true, config: {} },
		withPanel
	);
	assert.equal(drive.canSave, true);
	assert.equal(drive.canDisconnect, true);
	assert.match(drive.disconnectWarning, /disconnects Google Drive/);
	assert.match(drive.disconnectWarning, /Files you already added stay/);
	assert.doesNotMatch(drive.disconnectWarning, /indexed data|workspace/);

	const nextcloud = editViewActions(
		{ connector_type: "NEXTCLOUD_CONNECTOR", is_indexable: false, config: {} },
		withPanel
	);
	assert.equal(nextcloud.canSave, true);
	assert.match(nextcloud.disconnectWarning, /disconnects Nextcloud/);
	assert.match(nextcloud.disconnectWarning, /stay/);

	const crawler = editViewActions(
		{ connector_type: "WEBCRAWLER_CONNECTOR", is_indexable: true, config: {} },
		withPanel
	);
	assert.match(crawler.disconnectWarning, /deletes the documents it brought in/);
	assert.doesNotMatch(crawler.disconnectWarning, /workspace/);
});

test("an MCP server keeps its own wording", () => {
	const mcp = editViewActions(
		{
			connector_type: "MCP_CONNECTOR",
			is_indexable: false,
			config: { server_config: { name: "My tools", url: "https://mcp.example.com" } },
		},
		withPanel
	);
	assert.equal(mcp.canDisconnect, true);
	assert.equal(mcp.canSave, false);
	assert.equal(mcp.disconnectWarning, "Your agent will lose access to this service");
});

test("a connection's PUT names the connection and no workspace", async (t) => {
	const put = mock.method(baseApiService, "put", async () => ({}));
	t.after(() => put.mock.restore());

	await connectorsApiService.updateConnector({
		id: 5,
		data: { periodic_indexing_enabled: false, indexing_frequency_minutes: null },
	});
	assert.equal(put.mock.calls[0].arguments[0], "/api/v1/search-source-connectors/5");
	assert.deepEqual(put.mock.calls[0].arguments[2], {
		body: { periodic_indexing_enabled: false, indexing_frequency_minutes: null },
	});
});

const listed = {
	id: 5,
	name: "Nextcloud",
	connector_type: "NEXTCLOUD_CONNECTOR",
	enabled: false,
	is_indexable: false,
	last_indexed_at: null,
	config: {},
	enable_vision_llm: false,
	periodic_indexing_enabled: false,
	indexing_frequency_minutes: null,
	next_scheduled_at: null,
	user_id: "owner",
	created_at: "2026-10-01T00:00:00Z",
};

test("switching a connection is one PUT to its enabled route, either way", async (t) => {
	const put = mock.method(baseApiService, "put", async () => listed);
	const post = mock.method(baseApiService, "post", async () => listed);
	const del = mock.method(baseApiService, "delete", async () => listed);
	t.after(() => mock.restoreAll());

	await connectorsApiService.setConnectorEnabled({ connectorId: 5, enabled: false });
	await connectorsApiService.setConnectorEnabled({ connectorId: 5, enabled: true });

	assert.deepEqual(
		put.mock.calls.map((call) => [call.arguments[0], call.arguments[2]]),
		[
			["/api/v1/search-source-connectors/5/enabled", { body: { enabled: false } }],
			["/api/v1/search-source-connectors/5/enabled", { body: { enabled: true } }],
		]
	);
	// One idempotent PUT: never a POST to switch on and a DELETE to switch off.
	assert.equal(post.mock.callCount(), 0);
	assert.equal(del.mock.callCount(), 0);
});

test("the lists, the test and the policy are asked without a workspace id", async (t) => {
	const get = mock.method(baseApiService, "get", async () => []);
	const post = mock.method(baseApiService, "post", async () => ({}));
	t.after(() => mock.restoreAll());

	await connectorsApiService.getConnectors({});
	await connectorsApiService.getConnectors({ queryParams: { skip: 0, limit: 10 } });
	await connectorsApiService.getMCPConnectors();
	await connectorsApiService.getMCPPolicy();
	assert.deepEqual(
		get.mock.calls.map((call) => call.arguments[0]),
		[
			"/api/v1/search-source-connectors",
			"/api/v1/search-source-connectors?skip=0&limit=10",
			"/api/v1/connectors/mcp",
			"/api/v1/connectors/mcp/policy",
		]
	);

	const server = { url: "https://mcp.example.com", headers: {}, transport: "streamable-http" };
	await connectorsApiService.testMCPConnection({ ...server, transport: "streamable-http" }, 5);
	await connectorsApiService.createMCPConnector({
		data: { name: "My tools", server_config: { ...server, transport: "streamable-http" } },
	});
	await connectorsApiService.indexConnector({ connector_id: 5 });
	await connectorsApiService.indexConnector({
		connector_id: 5,
		queryParams: { start_date: "2026-10-01", end_date: "2026-11-01" },
	});
	assert.deepEqual(
		post.mock.calls.map((call) => call.arguments[0]),
		[
			"/api/v1/connectors/mcp/test",
			"/api/v1/connectors/mcp",
			"/api/v1/search-source-connectors/5/index",
			"/api/v1/search-source-connectors/5/index?start_date=2026-10-01&end_date=2026-11-01",
		]
	);
	// The saved server's id still goes in the test's body, so its masked keys work.
	assert.equal(
		(post.mock.calls[0].arguments[2] as { body: { connector_id: number } }).body.connector_id,
		5
	);
});
