import assert from "node:assert/strict";
import test from "node:test";
import { QueryClient } from "@tanstack/react-query";
import {
	connectorDetailQuery,
	editableConnector,
	fetchesItsOwnConfig,
	refreshConnectorAfterChange,
} from "@/components/assistant-ui/connector-popup/connector-configs/views/editable-connector";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";
import { cacheKeys } from "@/lib/query-client/cache-keys";

// An MCP server's config changes outside its edit view (a tool trusted from
// a chat, a revoke in another tab), and such a change never reached a view
// opened on the cached list row. The view asks the server for its own copy
// when it opens and after a change.

function connector(fields: Partial<SearchSourceConnector> = {}): SearchSourceConnector {
	return {
		id: 5,
		name: "Linear",
		connector_type: "MCP_CONNECTOR",
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

const MASKED = {
	server_config: { url: "https://mcp.example.org", headers: { Authorization: "••••abcd" } },
	trusted_tools: ["search"],
};

test("an MCP server's view waits for the server's copy instead of opening empty", () => {
	const live = connector({ enabled: false });
	assert.ok(fetchesItsOwnConfig(live));
	assert.deepEqual(editableConnector(live, undefined), { connector: live, pending: true });

	const fetched = connector({ config: MASKED, enable_vision_llm: true, enabled: true });
	const { connector: shown, pending } = editableConnector(live, fetched);
	assert.equal(pending, false);
	assert.deepEqual(shown.config, MASKED);
	assert.equal(shown.enable_vision_llm, true);
	// Only what the view edits is taken from the answer; the rest stays the row's.
	assert.equal(shown.enabled, false);
});

test("a failed request shows the row rather than a spinner forever", () => {
	const live = connector();
	assert.deepEqual(editableConnector(live, undefined, true), { connector: live, pending: false });
	// Another connector's answer (a reused cache entry) is not this one's.
	assert.equal(editableConnector(live, connector({ id: 6, config: MASKED }), false).pending, true);
});

test("a connection that is not an MCP server is shown as listed, with no request", () => {
	const drive = connector({
		id: 9,
		connector_type: "GOOGLE_DRIVE_CONNECTOR",
		config: { selected_folders: [{ id: "f", name: "F" }] },
		enable_vision_llm: true,
	});
	assert.equal(fetchesItsOwnConfig(drive), false);
	assert.deepEqual(editableConnector(drive, undefined), { connector: drive, pending: false });
	assert.equal(fetchesItsOwnConfig(connector({ id: 0 })), false);
});

test("the request is made at every opening, never on a window focus", () => {
	const query = connectorDetailQuery(5);
	assert.deepEqual(query.queryKey, cacheKeys.connectors.byId("5"));
	assert.equal(query.staleTime, 0);
	assert.equal(query.refetchOnMount, "always");
	assert.equal(query.refetchOnWindowFocus, false);
});

test("after a change the list and the connector's own copy are asked again", async () => {
	const client = new QueryClient();
	client.setQueryData(cacheKeys.connectors.all(), []);
	client.setQueryData(cacheKeys.connectors.byId("5"), connector({ config: MASKED }));
	client.setQueryData(cacheKeys.connectors.byId("6"), connector({ id: 6 }));
	await refreshConnectorAfterChange(client, 5);
	assert.equal(client.getQueryState(cacheKeys.connectors.all())?.isInvalidated, true);
	assert.equal(client.getQueryState(cacheKeys.connectors.byId("5"))?.isInvalidated, true);
	assert.equal(client.getQueryState(cacheKeys.connectors.byId("6"))?.isInvalidated, false);
	client.clear();
});

test("asking the list again leaves an open Google Drive folder alone", () => {
	// The list's key must not be a prefix of the folder keys, or every save
	// would refetch the folder tree that is open.
	const list = cacheKeys.connectors.all();
	const folders = cacheKeys.connectors.googleDrive.folders(9, "root");
	assert.notDeepEqual(folders.slice(0, list.length), [...list]);
});
