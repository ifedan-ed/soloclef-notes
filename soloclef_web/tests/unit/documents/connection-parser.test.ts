import assert from "node:assert/strict";
import test from "node:test";

import { connectorParser, setConnectorParserRequest } from "@/contracts/types/connector.types";
import { connectionParserChoice } from "@/features/parser-choice/connection-parser";
import { unavailableReasonKey } from "@/features/parser-choice/parser-labels";
import { capabilities } from "./parser-capabilities.fixture";

test("a connection with no stored parser shows none chosen, not the server default", () => {
	const choice = connectionParserChoice(capabilities(), null);
	assert.equal(choice.effectiveParser, undefined);
	assert.equal(choice.storedIsUnavailable, false);
	assert.equal(choice.options.length, 4);
});

test("a stored parser is shown as stored", () => {
	const choice = connectionParserChoice(capabilities(), "llamaparse");
	assert.equal(choice.effectiveParser, "llamaparse");
	assert.equal(choice.storedIsUnavailable, false);
});

test("a stored parser that has lost its key is still shown, and flagged", () => {
	const choice = connectionParserChoice(capabilities(), "datalab");
	assert.equal(choice.effectiveParser, "datalab");
	assert.equal(choice.storedIsUnavailable, true);
});

test("nothing is shown before the capabilities arrive", () => {
	assert.deepEqual(connectionParserChoice(undefined, "local"), {
		options: [],
		effectiveParser: undefined,
		storedIsUnavailable: false,
	});
});

test("only the server's own token is worded as 'not configured'", () => {
	assert.equal(unavailableReasonKey("not_configured"), "parser_unavailable_not_configured");
	assert.equal(unavailableReasonKey("quota_exhausted"), "parser_unavailable_other");
	assert.equal(unavailableReasonKey(null), "parser_unavailable_other");
});

test("the parser route's answer carries a parser or null, and nothing unknown", () => {
	assert.equal(connectorParser.parse({ id: 12, parser: "datalab" }).parser, "datalab");
	assert.equal(connectorParser.parse({ id: 12, parser: null }).parser, null);
	assert.equal(connectorParser.safeParse({ id: 12, parser: "cloud" }).success, false);
	// A missing parser is not "none chosen": the answer must say null outright.
	assert.equal(connectorParser.safeParse({ id: 12 }).success, false);
});

test("the save request takes one of the four parsers and nothing else", () => {
	assert.equal(
		setConnectorParserRequest.safeParse({ connectorId: 12, parser: "unstructured" }).success,
		true
	);
	assert.equal(setConnectorParserRequest.safeParse({ connectorId: 12, parser: "" }).success, false);
	assert.equal(
		setConnectorParserRequest.safeParse({ connectorId: 12, parser: "Local" }).success,
		false
	);
});

test("both parser calls go to the connection's own parser route and check the answer", async () => {
	// Read from the source: the calls are one-liners over the shared request
	// helper, and what can go wrong is the path or a dropped response schema
	// (an unchecked answer would let a missing `parser` through as undefined).
	const { readFileSync } = await import("node:fs");
	const source = readFileSync(
		new URL("../../../lib/apis/connectors-api.service.ts", import.meta.url),
		"utf8"
	).replace(/\s+/g, " ");
	assert.ok(
		source.includes(
			"baseApiService.put( `/api/v1/search-source-connectors/${connectorId}/parser`, connectorParser, { body: { parser } } )"
		)
	);
	assert.ok(
		source.includes(
			"baseApiService.get(`/api/v1/search-source-connectors/${connectorId}/parser`, connectorParser)"
		)
	);
});
