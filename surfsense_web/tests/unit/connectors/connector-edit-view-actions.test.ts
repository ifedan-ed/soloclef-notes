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
	is_mine: true,
	is_indexable: false,
	config: { accounts: [{ id: "a" }] },
} as const;

test("your mail's settings have the switch only: no Save, no Disconnect, a pointer to the card", () => {
	const actions = editViewActions(email, noPanel);
	assert.equal(actions.readOnly, false);
	assert.equal(actions.canSave, false);
	assert.equal(actions.canDisconnect, false);
	assert.equal(actions.mailboxesNote, EMAIL_MAILBOXES_NOTE);
	assert.match(actions.mailboxesNote ?? "", /Email \(IMAP\) card/);
	assert.doesNotMatch(actions.subtitle, /sync configuration/);
});

test("another member's connection opens read-only", () => {
	for (const type of [
		"EMAIL_IMAP_CONNECTOR",
		"GOOGLE_DRIVE_CONNECTOR",
		"NEXTCLOUD_CONNECTOR",
	] as const) {
		const actions = editViewActions(
			{ connector_type: type, is_mine: false, is_indexable: true, config: {} },
			withPanel
		);
		assert.equal(actions.readOnly, true, type);
		assert.equal(actions.canSave, false, type);
		assert.equal(actions.canDisconnect, false, type);
		assert.equal(actions.mailboxesNote, null, type);
		assert.match(actions.subtitle, /Another member's connection/, type);
	}
});

test("Disconnect on an account connection says it leaves every workspace", () => {
	const drive = editViewActions(
		{ connector_type: "GOOGLE_DRIVE_CONNECTOR", is_mine: true, is_indexable: true, config: {} },
		withPanel
	);
	assert.equal(drive.canSave, true);
	assert.equal(drive.canDisconnect, true);
	assert.match(drive.disconnectWarning, /every workspace/);
	assert.match(drive.disconnectWarning, /Files you already added stay/);
	assert.doesNotMatch(drive.disconnectWarning, /indexed data/);

	const nextcloud = editViewActions(
		{ connector_type: "NEXTCLOUD_CONNECTOR", is_mine: true, is_indexable: false, config: {} },
		withPanel
	);
	assert.equal(nextcloud.canSave, true);
	assert.match(nextcloud.disconnectWarning, /Nextcloud from every workspace/);
	assert.match(nextcloud.disconnectWarning, /stay/);
});

test("an MCP server keeps its own wording and is never read-only", () => {
	const mcp = editViewActions(
		{
			connector_type: "MCP_CONNECTOR",
			is_mine: false,
			is_indexable: false,
			config: { server_config: { name: "Team tools", url: "https://mcp.example.com" } },
		},
		withPanel
	);
	assert.equal(mcp.readOnly, false);
	assert.equal(mcp.canDisconnect, true);
	assert.equal(mcp.canSave, false);
	assert.equal(mcp.disconnectWarning, "Your agent will lose access to this service");
});

test("a connection's PUT carries the workspace its settings belong to", async (t) => {
	const put = mock.method(baseApiService, "put", async () => ({}));
	t.after(() => put.mock.restore());

	await connectorsApiService.updateConnector({
		id: 5,
		queryParams: { workspace_id: 7 },
		data: { periodic_indexing_enabled: false, indexing_frequency_minutes: null },
	});
	assert.equal(put.mock.calls[0].arguments[0], "/api/v1/search-source-connectors/5?workspace_id=7");

	// Without queryParams none is sent (updateConnectorMutationAtom adds the
	// active workspace to every edit made from the Connectors page).
	await connectorsApiService.updateConnector({ id: 6, data: { name: "Renamed" } });
	assert.equal(put.mock.calls[1].arguments[0], "/api/v1/search-source-connectors/6");
});
