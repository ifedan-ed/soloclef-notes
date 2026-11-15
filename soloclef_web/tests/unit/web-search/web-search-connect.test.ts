import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { connectableSource, connectLinkFor } from "@/features/notebooks/lib/research-connect-link";
import {
	connectionOf,
	webSearchConnectApi,
} from "@/features/web-search-connect/api/web-search-connect-api";
import {
	isResearchSourcesKey,
	providerToConnect,
	webSearchConnectPath,
} from "@/features/web-search-connect/model/web-search-providers";
import { baseApiService } from "@/lib/apis/base-api.service";

test("the key is sent once, to its provider's route, and removed by DELETE", async (t) => {
	const saved = {
		provider: "exa",
		connected: true,
		key_mask: "••••abcd",
		connected_at: "2026-11-12T10:00:00Z",
	};
	const put = mock.method(baseApiService, "put", async () => saved);
	const del = mock.method(baseApiService, "delete", async () => null);
	const get = mock.method(baseApiService, "get", async () => ({ providers: [saved] }));
	t.after(() => mock.restoreAll());

	assert.deepEqual(await webSearchConnectApi.connect("exa", "exa-key"), saved);
	assert.equal(put.mock.calls[0].arguments[0], "/api/v1/connectors/web-search/exa");
	assert.deepEqual(put.mock.calls[0].arguments[2], { body: { api_key: "exa-key" } });

	await webSearchConnectApi.disconnect("tavily");
	assert.equal(del.mock.calls[0].arguments[0], "/api/v1/connectors/web-search/tavily");

	assert.deepEqual(await webSearchConnectApi.list(), [saved]);
	assert.equal(get.mock.calls[0].arguments[0], "/api/v1/connectors/web-search");
});

test("a provider missing from the list, or a list not yet in, is not connected", () => {
	assert.deepEqual(connectionOf(undefined, "tavily"), {
		provider: "tavily",
		connected: false,
		key_mask: null,
		connected_at: null,
	});
});

test("the research box's Connect leads to that provider's card, and only for a missing key", () => {
	assert.equal(connectableSource("exa", "not_connected"), "exa");
	assert.equal(connectableSource("tavily", "not_connected"), "tavily");
	// Nothing to connect: a source this server does not search, or a scholarly one.
	assert.equal(connectableSource("exa", "not_available"), null);
	assert.equal(connectableSource("pubmed", "not_connected"), null);
	assert.equal(connectableSource("exa", undefined), null);
	assert.equal(connectLinkFor("exa"), "/dashboard/settings/connectors?connect=exa");
	assert.equal(
		providerToConnect(new URLSearchParams(webSearchConnectPath("tavily").split("?")[1])),
		"tavily"
	);
	assert.equal(providerToConnect(new URLSearchParams("connect=bing")), null);
});

test("a key change refreshes every notebook's research status and nothing else", () => {
	assert.equal(isResearchSourcesKey(["notebooks", 3, "research-sources"]), true);
	assert.equal(isResearchSourcesKey(["notebooks", 3, "sources"]), false);
	assert.equal(isResearchSourcesKey(["notebooks"]), false);
});
