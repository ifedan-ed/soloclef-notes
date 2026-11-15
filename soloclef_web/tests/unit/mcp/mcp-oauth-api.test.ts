import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { baseApiService } from "@/lib/apis/base-api.service";
import { connectorsApiService } from "@/lib/apis/connectors-api.service";

test("OAuth discovery asks about the typed URL, naming the saved server only when there is one", async (t) => {
	const post = mock.method(baseApiService, "post", async () => ({}));
	t.after(() => mock.restoreAll());

	await connectorsApiService.discoverMCPOAuth("https://mcp.example.com/mcp");
	await connectorsApiService.discoverMCPOAuth("https://mcp.example.com/mcp", 7);
	await connectorsApiService.startMCPOAuth(7, "/dashboard/settings/connectors");
	assert.deepEqual(
		post.mock.calls.map((call) => [call.arguments[0], call.arguments[2]]),
		[
			["/api/v1/connectors/mcp/oauth/discover", { body: { url: "https://mcp.example.com/mcp" } }],
			[
				"/api/v1/connectors/mcp/oauth/discover",
				{ body: { url: "https://mcp.example.com/mcp", connector_id: 7 } },
			],
			[
				"/api/v1/connectors/mcp/7/oauth/start",
				{ body: { return_path: "/dashboard/settings/connectors" } },
			],
		]
	);
});

test("a test sends the new settings shape, with the saved server's id beside them", async (t) => {
	const post = mock.method(baseApiService, "post", async () => ({}));
	t.after(() => mock.restoreAll());
	const settings = {
		url: "https://mcp.example.com/mcp",
		transport: "sse" as const,
		auth: { type: "bearer" as const, token: "••••abcd" },
		headers: { "X-Org": "acme" },
	};
	await connectorsApiService.testMCPConnection(settings, 3);
	assert.deepEqual(post.mock.calls[0].arguments[2], { body: { ...settings, connector_id: 3 } });
});
