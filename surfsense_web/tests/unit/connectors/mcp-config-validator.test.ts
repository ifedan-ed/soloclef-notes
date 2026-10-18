import assert from "node:assert/strict";
import test from "node:test";
import { parseMCPConfig } from "@/components/assistant-ui/connector-popup/utils/mcp-config-validator";
import { mcpServerConfig } from "@/contracts/types/mcp.types";

test("a local (stdio) MCP config is refused", () => {
	for (const cfg of [
		{ command: "python", args: ["-c", "print(1)"] },
		{ command: "npx", transport: "stdio" },
		{ url: "https://mcp.exa.ai/mcp", transport: "stdio" },
	]) {
		const result = parseMCPConfig(JSON.stringify(cfg));
		assert.equal(result.config, null);
		assert.match(result.error ?? "", /not supported|invalid/i);
	}
});

test("only https server URLs are accepted", () => {
	assert.equal(parseMCPConfig(JSON.stringify({ url: "http://mcp.exa.ai/mcp" })).config, null);
	const ok = parseMCPConfig(
		JSON.stringify({ name: "Exa", url: "https://mcp.exa.ai/mcp", headers: { "x-api-key": "k" } })
	);
	assert.deepEqual(ok.config, {
		url: "https://mcp.exa.ai/mcp",
		headers: { "x-api-key": "k" },
		transport: "streamable-http",
	});
});

test("a masked header from the server parses and is sent back unchanged", () => {
	const read = mcpServerConfig.parse({
		url: "https://mcp.browserbase.com/mcp",
		headers: { Authorization: "••••7777" },
		transport: "streamable-http",
	});
	assert.equal(read.headers.Authorization, "••••7777");
});
