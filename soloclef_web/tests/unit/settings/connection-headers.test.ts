import assert from "node:assert/strict";
import test from "node:test";

import {
	headersFromRows,
	savedHeadersIntent,
} from "@/components/settings/model-connections/connection-headers";
import {
	connectionUpdateFor,
	hasConnectionChanges,
} from "@/components/settings/model-connections/saved-key-draft";
import {
	connectionCreateRequest,
	connectionRead,
	connectionUpdateRequest,
} from "@/contracts/types/model-connections.types";

// Run with: pnpm exec tsx --test tests/unit/settings/connection-headers.test.ts
//
// The extra headers of an OpenAI-compatible connection are held like its key:
// the server answers their names and never their values, so the editor sends
// `headers` only to replace or remove them. Every value here is invented.

const VALUE = "fake-header-value-not-a-credential";

test("rows become headers, trimmed, with an untouched blank row ignored", () => {
	assert.deepEqual(
		headersFromRows([
			{ name: " X-Org ", value: ` ${VALUE} ` },
			{ name: "", value: "" },
		]),
		{ headers: { "X-Org": VALUE }, problem: null }
	);
	assert.deepEqual(headersFromRows([]), { headers: {}, problem: null });
});

test("a half-filled row is said, not dropped", () => {
	assert.match(headersFromRows([{ name: "X-Org", value: "" }]).problem ?? "", /name and a value/);
	assert.match(headersFromRows([{ name: "", value: VALUE }]).problem ?? "", /name and a value/);
});

test("the headers the server sets itself are refused, whatever their case", () => {
	for (const name of ["Authorization", "content-type", "ACCEPT", "Host", "Content-Length"]) {
		assert.match(
			headersFromRows([{ name, value: VALUE }]).problem ?? "",
			/set by the server itself/,
			name
		);
	}
});

test("a name that is not an HTTP token, and a name given twice, are refused", () => {
	assert.match(headersFromRows([{ name: "X Org", value: VALUE }]).problem ?? "", /not a valid/);
	assert.match(headersFromRows([{ name: "X-Org:", value: VALUE }]).problem ?? "", /not a valid/);
	assert.match(
		headersFromRows([
			{ name: "X-Org", value: VALUE },
			{ name: "x-org", value: VALUE },
		]).problem ?? "",
		/given twice/
	);
});

test("typed headers replace, a removal sends null, and nothing typed keeps them", () => {
	assert.deepEqual(savedHeadersIntent({}, false), { kind: "keep" });
	assert.deepEqual(savedHeadersIntent({}, true), { kind: "remove" });
	// Typing after asking for removal means "these instead".
	assert.deepEqual(savedHeadersIntent({ "X-Org": VALUE }, true), {
		kind: "replace",
		headers: { "X-Org": VALUE },
	});
});

test("the update carries headers only to replace or remove them", () => {
	const keep = connectionUpdateFor("https://api.example.test/v1", { kind: "keep" });
	// Absent, not {} and not null: either would change the saved headers.
	assert.equal(Object.hasOwn(keep, "headers"), false);
	assert.deepEqual(
		connectionUpdateFor(
			"https://api.example.test/v1",
			{ kind: "keep" },
			{ kind: "replace", headers: { "X-Org": VALUE } }
		).headers,
		{ "X-Org": VALUE }
	);
	assert.equal(
		connectionUpdateFor("https://api.example.test/v1", { kind: "keep" }, { kind: "remove" })
			.headers,
		null
	);
	// The schema the request is parsed with keeps each of the three as it is.
	assert.equal(Object.hasOwn(connectionUpdateRequest.parse(keep), "headers"), false);
	assert.equal(connectionUpdateRequest.parse({ headers: null }).headers, null);
});

test("a change of headers alone is a change worth saving", () => {
	const base = "https://api.example.test/v1";
	assert.equal(hasConnectionChanges(base, base, { kind: "keep" }), false);
	assert.equal(hasConnectionChanges(base, base, { kind: "keep" }, { kind: "remove" }), true);
});

test("a new connection may carry headers, and a read connection names them only", () => {
	const created = connectionCreateRequest.parse({
		provider: "openai_compatible",
		base_url: "https://api.example.test/v1",
		api_key: "fake-key-not-a-real-credential",
		headers: { "X-Org": VALUE },
		scope: "user",
	});
	assert.deepEqual(created.headers, { "X-Org": VALUE });
	const read = connectionRead.parse({
		id: 1,
		provider: "openai_compatible",
		scope: "user",
		enabled: true,
		has_api_key: true,
		header_names: ["X-Org"],
		// A value sent by mistake is not kept by the parse.
		headers: { "X-Org": VALUE },
	});
	assert.deepEqual(read.header_names, ["X-Org"]);
	assert.equal(JSON.stringify(read).includes(VALUE), false);
});
