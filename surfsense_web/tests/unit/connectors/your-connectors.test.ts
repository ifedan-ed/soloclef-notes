import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
	accountsListFor,
	connectsNothing,
	groupConnectorsByType,
} from "@/components/assistant-ui/connector-popup/constants/connector-constants";
import type { ConnectorRow } from "@/components/assistant-ui/connector-popup/hooks/use-connector-rows";
import { EnabledInWorkspaceSwitch } from "@/components/assistant-ui/connector-popup/workspace-enablement/enabled-in-workspace-switch";
import {
	accountOwnerLabel,
	showsIndexedLine,
} from "@/components/assistant-ui/connector-popup/your-connectors/account-card";
import { ConnectorRowStatus } from "@/components/assistant-ui/connector-popup/your-connectors/connector-row-status";
import {
	liveRowsChanged,
	liveRowsKey,
	workspaceConnectorList,
} from "@/components/assistant-ui/connector-popup/your-connectors/workspace-connector-list";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";

function connector(
	fields: Pick<SearchSourceConnector, "id" | "connector_type"> & Partial<SearchSourceConnector>
): SearchSourceConnector {
	return {
		name: fields.connector_type,
		is_indexable: false,
		is_active: true,
		last_indexed_at: null,
		config: {},
		enable_vision_llm: false,
		periodic_indexing_enabled: false,
		indexing_frequency_minutes: null,
		next_scheduled_at: null,
		workspace_id: null,
		user_id: "owner",
		created_at: "2026-10-01T00:00:00Z",
		...fields,
	};
}

// What the two sources hold for a workspace (id 7) with one MCP server, while
// its owner also has a mailbox and Nextcloud: Zero syncs only rows whose
// workspace_id is 7; the REST list has those plus every account-wide
// connection usable here, with its on/off state and owner flag.
// Zero never syncs config (it holds the server's headers and tokens): a live
// row has none, and the REST list's copy has it with secrets masked.
const liveMcp = connector({
	id: 1,
	connector_type: "MCP_CONNECTOR",
	name: "Team tools",
	workspace_id: 7,
});
const listedMcp = connector({
	...liveMcp,
	config: {
		server_config: {
			name: "Team tools",
			url: "https://mcp.example.com",
			headers: { Authorization: "Bearer ****" },
		},
	},
	is_mine: true,
});
const listedEmail = connector({
	id: 2,
	connector_type: "EMAIL_IMAP_CONNECTOR",
	name: "Email (IMAP)",
	enabled_in_workspace: true,
	is_mine: true,
});
const listedNextcloud = connector({
	id: 3,
	connector_type: "NEXTCLOUD_CONNECTOR",
	name: "Nextcloud",
	enabled_in_workspace: true,
	is_mine: true,
});

const ids = (list: SearchSourceConnector[]) => list.map((c) => c.id).sort((a, b) => a - b);

function row(connectors: SearchSourceConnector[]): ConnectorRow {
	const [grouped] = groupConnectorsByType(connectors);
	return { ...grouped, health: "ok" };
}

test("a workspace with an MCP server still lists the account's mailbox and Nextcloud", () => {
	const list = workspaceConnectorList([liveMcp], [listedMcp, listedEmail, listedNextcloud]);
	assert.deepEqual(ids(list), [1, 2, 3]);
	assert.deepEqual(
		groupConnectorsByType(list).map((r) => r.title),
		["Email (IMAP)", "MCP Servers", "Nextcloud"]
	);
});

test("a connection in both sources is listed once, as its live row", () => {
	const list = workspaceConnectorList([liveMcp], [listedMcp, listedEmail]);
	const mcps = list.filter((c) => c.id === liveMcp.id);
	assert.equal(mcps.length, 1);
	// Its config is the REST copy's, masked: Zero has none to give.
	assert.deepEqual(mcps[0].config, listedMcp.config);
	// The owner flag only the REST list computes is kept on the live row.
	assert.equal(mcps[0].is_mine, true);
});

test("an account connection switched off here keeps its off state", () => {
	const off = { ...listedEmail, enabled_in_workspace: false };
	const list = workspaceConnectorList([liveMcp], [listedMcp, off]);
	assert.equal(list.find((c) => c.id === off.id)?.enabled_in_workspace, false);
});

test("a workspace connector removed live is gone before the REST list refreshes", () => {
	const removed = connector({ id: 9, connector_type: "MCP_CONNECTOR", workspace_id: 7 });
	const list = workspaceConnectorList([liveMcp], [removed, listedMcp, listedEmail]);
	assert.deepEqual(ids(list), [1, 2]);
});

test("with no live rows (loading, none, or refused), the REST list is used as it is", () => {
	const list = workspaceConnectorList([], [listedMcp, listedEmail, listedNextcloud]);
	assert.deepEqual(ids(list), [1, 2, 3]);
	assert.deepEqual(workspaceConnectorList([liveMcp], undefined), [liveMcp]);
});

test("each row counts the accounts switched on here", () => {
	const driveOn = connector({ id: 4, connector_type: "GOOGLE_DRIVE_CONNECTOR" });
	const driveOff = connector({
		id: 5,
		connector_type: "GOOGLE_DRIVE_CONNECTOR",
		enabled_in_workspace: false,
	});
	const [email, drive] = groupConnectorsByType([
		driveOn,
		driveOff,
		{ ...listedEmail, enabled_in_workspace: false },
	]);
	assert.equal(drive.title, "Google Drive");
	assert.deepEqual([drive.accountCount, drive.enabledCount], [2, 1]);
	assert.deepEqual([email.accountCount, email.enabledCount], [1, 0]);
	// MCP servers have no per-workspace switch: always on where they are.
	assert.equal(row([liveMcp]).enabledCount, 1);
});

test("Your connectors shows Off for a connection switched off here", () => {
	const html = renderToStaticMarkup(
		createElement(ConnectorRowStatus, {
			row: row([{ ...listedEmail, enabled_in_workspace: false }]),
		})
	);
	assert.match(html, />Off</);
	assert.match(html, /Enabled in this workspace: off/);
});

test("Your connectors shows nothing extra for one connection that is on", () => {
	const html = renderToStaticMarkup(createElement(ConnectorRowStatus, { row: row([listedEmail]) }));
	assert.equal(html, "");
});

test("Your connectors says how many of a type's accounts are on here", () => {
	const html = renderToStaticMarkup(
		createElement(ConnectorRowStatus, {
			row: row([
				connector({ id: 4, connector_type: "GOOGLE_DRIVE_CONNECTOR" }),
				connector({ id: 5, connector_type: "GOOGLE_DRIVE_CONNECTOR", enabled_in_workspace: false }),
			]),
		})
	);
	assert.match(html, />1 of 2 on</);
});

test("the on/off switch is labelled Enabled in this workspace", () => {
	const html = renderToStaticMarkup(
		createElement(EnabledInWorkspaceSwitch, { checked: false, onCheckedChange: () => undefined })
	);
	assert.match(html, />Enabled in this workspace</);
	assert.match(html, /role="switch"/);
	assert.match(html, /aria-label="Enabled in this workspace"/);
	assert.match(html, /aria-checked="false"/);
});

test("the last MCP server removed elsewhere leaves the list once it is refetched", () => {
	// Zero had the one server; a teammate deletes it, Zero completes with [].
	const before = liveRowsKey([liveMcp]);
	const stale = [listedMcp, listedEmail];
	// Until the REST list is refetched it is all there is, so the server shows...
	assert.deepEqual(ids(workspaceConnectorList([], stale)), [1, 2]);
	// ...but the change in Zero's rows is what makes the panel refetch it,
	assert.equal(liveRowsChanged(before, []), true);
	// and the refetched list no longer has it.
	assert.deepEqual(ids(workspaceConnectorList([], [listedEmail])), [2]);
});

test("the REST list is refetched only when Zero's set of rows changes", () => {
	const second = connector({ id: 8, connector_type: "MCP_CONNECTOR", workspace_id: 7 });
	// Nothing to compare with before the first complete result.
	assert.equal(liveRowsChanged(null, [liveMcp]), false);
	const key = liveRowsKey([liveMcp, second]);
	// The same rows, in another order or with new field values: no refetch.
	assert.equal(liveRowsChanged(key, [second, { ...liveMcp, name: "Renamed" }]), false);
	// A server added or removed: refetch.
	assert.equal(liveRowsChanged(key, [liveMcp]), true);
	assert.equal(
		liveRowsChanged(key, [liveMcp, second, connector({ id: 9, connector_type: "MCP_CONNECTOR" })]),
		true
	);
});

test("an Email connection whose last mailbox was removed is not listed", () => {
	const empty = { ...listedEmail, config: { accounts: [] } };
	const oneMailbox = { ...listedEmail, config: { accounts: [{ id: "a", username: "me@x.org" }] } };
	assert.equal(connectsNothing(empty), true);
	assert.equal(connectsNothing(oneMailbox), false);
	// A row without the mailbox list (not from the REST list) is kept.
	assert.equal(connectsNothing(listedEmail), false);
	assert.deepEqual(
		groupConnectorsByType([liveMcp, empty]).map((r) => r.title),
		["MCP Servers"]
	);
	assert.deepEqual(
		groupConnectorsByType([liveMcp, oneMailbox]).map((r) => r.title),
		["Email (IMAP)", "MCP Servers"]
	);
});

test("a shared workspace's mail cards say whose each one is", () => {
	const mine = listedEmail;
	const theirs = { ...listedEmail, id: 12, user_id: "teammate", is_mine: false };
	assert.equal(accountOwnerLabel(mine), "Yours");
	assert.equal(accountOwnerLabel(theirs), "Another member's");
	// MCP servers belong to the workspace; a row without is_mine says nothing.
	assert.equal(accountOwnerLabel({ ...listedMcp, is_mine: true }), null);
	assert.equal(accountOwnerLabel({ ...listedEmail, is_mine: undefined }), null);
	// The two cards sum up as "1 of 2 on" once yours is off.
	const [email] = groupConnectorsByType([{ ...mine, enabled_in_workspace: false }, theirs]);
	const html = renderToStaticMarkup(
		createElement(ConnectorRowStatus, { row: { ...email, health: "ok" } })
	);
	assert.match(html, />1 of 2 on</);
});

test("mail cards never say Never indexed; Drive cards still do", () => {
	assert.equal(showsIndexedLine(listedEmail), false);
	assert.equal(
		showsIndexedLine(connector({ id: 4, connector_type: "GOOGLE_DRIVE_CONNECTOR" })),
		true
	);
	assert.equal(showsIndexedLine(listedMcp), false);
});

test("the chat menus' row mark takes their text size", () => {
	const html = renderToStaticMarkup(
		createElement(ConnectorRowStatus, {
			row: row([{ ...listedEmail, enabled_in_workspace: false }]),
			className: "text-xs",
		})
	);
	assert.match(html, />Off</);
	assert.match(html, /class="[^"]*\btext-xs\b/);
	assert.doesNotMatch(html, /text-\[11px\]/);
});

test("a row with several mail or Nextcloud connections opens their list", () => {
	// Two members' mail in a shared workspace: the row must open a list, or
	// the owner's switch is out of reach (it opened nothing for non-OAuth types).
	assert.deepEqual(accountsListFor("EMAIL_IMAP_CONNECTOR", "Email (IMAP)"), {
		connectorType: "EMAIL_IMAP_CONNECTOR",
		connectorTitle: "Email (IMAP)",
	});
	assert.equal(accountsListFor("NEXTCLOUD_CONNECTOR")?.connectorTitle, "Nextcloud");
	assert.equal(accountsListFor("GOOGLE_DRIVE_CONNECTOR")?.connectorTitle, "Google Drive");
	// MCP servers have their own list.
	assert.equal(accountsListFor("MCP_CONNECTOR"), null);
});
