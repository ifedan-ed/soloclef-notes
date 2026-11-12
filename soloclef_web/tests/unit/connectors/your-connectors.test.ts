import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
	accountsListFor,
	connectsNothing,
	groupConnectorsByType,
} from "@/components/assistant-ui/connector-popup/constants/connector-constants";
import { EnabledSwitch } from "@/components/assistant-ui/connector-popup/enablement/enabled-switch";
import type { ConnectorRow } from "@/components/assistant-ui/connector-popup/hooks/use-connector-rows";
import { showsIndexedLine } from "@/components/assistant-ui/connector-popup/your-connectors/account-card";
import { ConnectorRowStatus } from "@/components/assistant-ui/connector-popup/your-connectors/connector-row-status";
import {
	liveRowsChanged,
	liveRowsKey,
} from "@/components/assistant-ui/connector-popup/your-connectors/live-rows";
import {
	getConnectorsResponse,
	type SearchSourceConnector,
} from "@/contracts/types/connector.types";

function connector(
	fields: Pick<SearchSourceConnector, "id" | "connector_type"> & Partial<SearchSourceConnector>
): SearchSourceConnector {
	return {
		name: fields.connector_type,
		enabled: true,
		is_indexable: false,
		is_active: true,
		last_indexed_at: null,
		config: {},
		enable_vision_llm: false,
		periodic_indexing_enabled: false,
		indexing_frequency_minutes: null,
		next_scheduled_at: null,
		user_id: "owner",
		created_at: "2026-10-01T00:00:00Z",
		...fields,
	};
}

// What GET /search-source-connectors answers for an account with one MCP
// server, a mailbox and Nextcloud: every connection of the account, of every
// type, each with its masked config and its own on/off state.
const listedMcp = connector({
	id: 1,
	connector_type: "MCP_CONNECTOR",
	name: "My tools",
	config: {
		server_config: {
			name: "My tools",
			url: "https://mcp.example.com",
			headers: { Authorization: "Bearer ****" },
		},
	},
});
const listedEmail = connector({
	id: 2,
	connector_type: "EMAIL_IMAP_CONNECTOR",
	name: "Email (IMAP)",
});
const listedNextcloud = connector({
	id: 3,
	connector_type: "NEXTCLOUD_CONNECTOR",
	name: "Nextcloud",
});

function row(connectors: SearchSourceConnector[]): ConnectorRow {
	const [grouped] = groupConnectorsByType(connectors);
	return { ...grouped, health: "ok" };
}

test("the one list holds the account's MCP server, mailbox and Nextcloud", () => {
	assert.deepEqual(
		groupConnectorsByType([listedMcp, listedEmail, listedNextcloud]).map((r) => r.title),
		["Email (IMAP)", "MCP Servers", "Nextcloud"]
	);
});

test("a listed connection carries enabled and nothing about a workspace or an owner flag", () => {
	const item = {
		id: 2,
		name: "Email (IMAP)",
		connector_type: "EMAIL_IMAP_CONNECTOR",
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
	// The fields the list used to carry are dropped if a server still sends them.
	const [read] = getConnectorsResponse.parse([
		{ ...item, workspace_id: 7, enabled_in_workspace: true, is_mine: false },
	]);
	assert.equal(read.enabled, false);
	for (const gone of ["workspace_id", "enabled_in_workspace", "is_mine"]) {
		assert.equal(Object.hasOwn(read, gone), false, gone);
	}
	// `enabled` is required: a row without it is refused, not guessed to be on.
	const { enabled: _enabled, ...withoutEnabled } = item;
	assert.equal(getConnectorsResponse.safeParse([withoutEnabled]).success, false);
});

test("each row counts the accounts switched on", () => {
	const driveOn = connector({ id: 4, connector_type: "GOOGLE_DRIVE_CONNECTOR" });
	const driveOff = connector({ id: 5, connector_type: "GOOGLE_DRIVE_CONNECTOR", enabled: false });
	const [email, drive] = groupConnectorsByType([
		driveOn,
		driveOff,
		{ ...listedEmail, enabled: false },
	]);
	assert.equal(drive.title, "Google Drive");
	assert.deepEqual([drive.accountCount, drive.enabledCount], [2, 1]);
	assert.deepEqual([email.accountCount, email.enabledCount], [1, 0]);
	// An MCP server is counted by the same field as any other connection.
	assert.equal(row([listedMcp]).enabledCount, 1);
	assert.equal(row([{ ...listedMcp, enabled: false }]).enabledCount, 0);
});

test("Your connectors shows Off for a connection that is switched off", () => {
	const html = renderToStaticMarkup(
		createElement(ConnectorRowStatus, {
			row: row([{ ...listedEmail, enabled: false }]),
		})
	);
	assert.match(html, />Off</);
	assert.match(html, /title="Enabled: off"/);
});

test("Your connectors shows nothing extra for one connection that is on", () => {
	const html = renderToStaticMarkup(createElement(ConnectorRowStatus, { row: row([listedEmail]) }));
	assert.equal(html, "");
});

test("Your connectors says how many of a type's accounts are on", () => {
	const html = renderToStaticMarkup(
		createElement(ConnectorRowStatus, {
			row: row([
				connector({ id: 4, connector_type: "GOOGLE_DRIVE_CONNECTOR" }),
				connector({ id: 5, connector_type: "GOOGLE_DRIVE_CONNECTOR", enabled: false }),
			]),
		})
	);
	assert.match(html, />1 of 2 on</);
});

test("the on/off switch is labelled Enabled", () => {
	const html = renderToStaticMarkup(
		createElement(EnabledSwitch, { checked: false, onCheckedChange: () => undefined })
	);
	assert.match(html, />Enabled</);
	assert.match(html, /role="switch"/);
	assert.match(html, /aria-label="Enabled"/);
	assert.match(html, /aria-checked="false"/);
	assert.doesNotMatch(html, /workspace/i);
});

test("the last MCP server removed in another tab makes the panel ask for the list again", () => {
	// Zero had the one server; it is deleted elsewhere and Zero completes with [].
	const before = liveRowsKey([listedMcp]);
	assert.equal(liveRowsChanged(before, []), true);
});

test("the REST list is refetched only when Zero's rows change", () => {
	const second = connector({ id: 8, connector_type: "MCP_CONNECTOR" });
	// Nothing to compare with before the first complete result.
	assert.equal(liveRowsChanged(null, [listedMcp]), false);
	const key = liveRowsKey([listedMcp, second]);
	// The same rows in another order: no refetch.
	assert.equal(liveRowsChanged(key, [second, listedMcp]), false);
	// Fields Zero does not sync play no part: the list is the source of them.
	assert.equal(liveRowsChanged(key, [{ ...listedMcp, config: {}, enabled: false }, second]), false);
	// A server added or removed: refetch.
	assert.equal(liveRowsChanged(key, [listedMcp]), true);
	assert.equal(
		liveRowsChanged(key, [
			listedMcp,
			second,
			connector({ id: 9, connector_type: "MCP_CONNECTOR" }),
		]),
		true
	);
	// The rows shown are the REST list's alone, so a synced field that changed
	// (a rename, a finished indexing run) is also a reason to ask again.
	assert.equal(liveRowsChanged(key, [{ ...listedMcp, name: "Renamed" }, second]), true);
	assert.equal(
		liveRowsChanged(key, [listedMcp, { ...second, last_indexed_at: "2026-11-11T09:00:00Z" }]),
		true
	);
});

test("an Email connection whose last mailbox was removed is not listed", () => {
	const empty = { ...listedEmail, config: { accounts: [] } };
	const oneMailbox = { ...listedEmail, config: { accounts: [{ id: "a", username: "me@x.org" }] } };
	assert.equal(connectsNothing(empty), true);
	assert.equal(connectsNothing(oneMailbox), false);
	// A row whose config has no mailbox list at all is kept.
	assert.equal(connectsNothing(listedEmail), false);
	assert.deepEqual(
		groupConnectorsByType([listedMcp, empty]).map((r) => r.title),
		["MCP Servers"]
	);
	assert.deepEqual(
		groupConnectorsByType([listedMcp, oneMailbox]).map((r) => r.title),
		["Email (IMAP)", "MCP Servers"]
	);
});

test("two mail connections of one account sum up as 1 of 2 on once one is off", () => {
	// Every card is the account's own, so no card says whose it is.
	const second = { ...listedEmail, id: 12 };
	const [email] = groupConnectorsByType([{ ...listedEmail, enabled: false }, second]);
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
			row: row([{ ...listedEmail, enabled: false }]),
			className: "text-xs",
		})
	);
	assert.match(html, />Off</);
	assert.match(html, /class="[^"]*\btext-xs\b/);
	assert.doesNotMatch(html, /text-\[11px\]/);
});

test("a row with several mail or Nextcloud connections opens their list", () => {
	// The list is where each connection's switch is, so a type without OAuth
	// opens one too (it opened nothing for those types once).
	assert.deepEqual(accountsListFor("EMAIL_IMAP_CONNECTOR", "Email (IMAP)"), {
		connectorType: "EMAIL_IMAP_CONNECTOR",
		connectorTitle: "Email (IMAP)",
	});
	assert.equal(accountsListFor("NEXTCLOUD_CONNECTOR")?.connectorTitle, "Nextcloud");
	assert.equal(accountsListFor("GOOGLE_DRIVE_CONNECTOR")?.connectorTitle, "Google Drive");
	// MCP servers have their own list.
	assert.equal(accountsListFor("MCP_CONNECTOR"), null);
});
