import assert from "node:assert/strict";
import test from "node:test";
import { QueryClient } from "@tanstack/react-query";
import {
	connectorDetailQuery,
	editableConnector,
	fetchesItsOwnConfig,
	refreshConnectorAfterChange,
} from "@/components/assistant-ui/connector-popup/connector-configs/views/editable-connector";
import { workspaceConnectorList } from "@/components/assistant-ui/connector-popup/your-connectors/workspace-connector-list";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";
import { cacheKeys } from "@/lib/query-client/cache-keys";

// Zero stopped carrying connector config (c0029): a workspace MCP server's
// live row has `{}` until the REST list has it, so its edit view opened on an
// empty form, and a change made elsewhere never reached an open view. The
// view now asks the server for its own copy when it opens and after a change.

function connector(fields: Partial<SearchSourceConnector> = {}): SearchSourceConnector {
	return {
		id: 5,
		name: "Linear",
		connector_type: "MCP_CONNECTOR",
		is_indexable: false,
		is_active: true,
		last_indexed_at: null,
		config: {},
		enable_vision_llm: false,
		periodic_indexing_enabled: false,
		indexing_frequency_minutes: null,
		next_scheduled_at: null,
		workspace_id: 7,
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
	const live = connector({ enabled_in_workspace: true, is_mine: true });
	assert.ok(fetchesItsOwnConfig(live));
	assert.deepEqual(editableConnector(live, undefined), { connector: live, pending: true });

	const fetched = connector({ config: MASKED, enable_vision_llm: false, is_mine: null });
	const { connector: shown, pending } = editableConnector(live, fetched);
	assert.equal(pending, false);
	assert.deepEqual(shown.config, MASKED);
	// What only the workspace list knows stays the row's.
	assert.equal(shown.is_mine, true);
	assert.equal(shown.enabled_in_workspace, true);
});

test("a failed request shows the row rather than a spinner forever", () => {
	const live = connector();
	assert.deepEqual(editableConnector(live, undefined, true), { connector: live, pending: false });
	// Another connector's answer (a reused cache entry) is not this one's.
	assert.equal(editableConnector(live, connector({ id: 6, config: MASKED }), false).pending, true);
});

test("an account-wide connection is shown as listed, with no request", () => {
	const drive = connector({
		id: 9,
		connector_type: "GOOGLE_DRIVE_CONNECTOR",
		workspace_id: null,
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
	client.setQueryData(cacheKeys.connectors.all("7"), []);
	client.setQueryData(cacheKeys.connectors.byId("5"), connector({ config: MASKED }));
	client.setQueryData(cacheKeys.connectors.byId("6"), connector({ id: 6 }));
	await refreshConnectorAfterChange(client, 7, 5);
	assert.equal(client.getQueryState(cacheKeys.connectors.all("7"))?.isInvalidated, true);
	assert.equal(client.getQueryState(cacheKeys.connectors.byId("5"))?.isInvalidated, true);
	assert.equal(client.getQueryState(cacheKeys.connectors.byId("6"))?.isInvalidated, false);
	client.clear();
});

test("a live row takes the listed copy's vision switch with its config", () => {
	const live = connector({ connector_type: "MCP_CONNECTOR" });
	const listed = connector({ config: MASKED, enable_vision_llm: true });
	const [row] = workspaceConnectorList([live], [listed]);
	assert.deepEqual(row.config, MASKED);
	assert.equal(row.enable_vision_llm, true);
});
