import assert from "node:assert/strict";
import test from "node:test";
import { createSseDataReader, parseResearchEvent } from "@/features/notebooks/lib/research-sse";

function readAll(chunks: string[]): string[] {
	const reader = createSseDataReader();
	return [...chunks.flatMap((chunk) => reader.push(chunk)), ...reader.end()];
}

test("whole events come out one payload each", () => {
	assert.deepEqual(readAll(['data: {"type":"done"}\n\ndata: {"a":1}\n\n']), [
		'{"type":"done"}',
		'{"a":1}',
	]);
});

test("an event split anywhere across chunks is reassembled", () => {
	const stream =
		'data: {"type":"phase","phase":"reading","progress":40}\n\ndata: {"type":"done"}\n\n';
	const expected = ['{"type":"phase","phase":"reading","progress":40}', '{"type":"done"}'];
	for (let cut = 1; cut < stream.length; cut++) {
		assert.deepEqual(readAll([stream.slice(0, cut), stream.slice(cut)]), expected, `cut at ${cut}`);
	}
	assert.deepEqual(readAll([...stream]), expected, "one character at a time");
});

test("CRLF line endings split between chunks still end the event once", () => {
	const reader = createSseDataReader();
	assert.deepEqual(reader.push("data: x\r"), []);
	assert.deepEqual(reader.push("\n\r"), []);
	assert.deepEqual(reader.push("\ndata: y\r\n\r\n"), ["x", "y"]);
	assert.deepEqual(reader.end(), []);
});

test("multi-line data joins with newlines; comments and other fields are ignored", () => {
	assert.deepEqual(readAll([": keep-alive\n\nevent: message\nid: 3\ndata: a\ndata:b\n\n"]), [
		"a\nb",
	]);
});

test("a final event without its blank line is still delivered when the stream ends", () => {
	assert.deepEqual(readAll(['data: {"type":"done"}']), ['{"type":"done"}']);
});

test("research events are validated; anything else is dropped", () => {
	assert.deepEqual(parseResearchEvent('{"type":"phase","phase":"ranking","progress":90}'), {
		type: "phase",
		phase: "ranking",
		progress: 90,
	});
	const results = parseResearchEvent(
		JSON.stringify({
			type: "results",
			results: [
				{
					id: "r1",
					url: "https://example.com",
					title: "Example",
					snippet: "Snippet",
					source: "web",
					thumbnail_url: null,
					meta: null,
				},
			],
		})
	);
	assert.equal(results?.type, "results");
	assert.deepEqual(parseResearchEvent('{"type":"error","message":"quota"}'), {
		type: "error",
		message: "quota",
	});
	assert.equal(parseResearchEvent("not json"), null);
	assert.equal(parseResearchEvent('{"type":"phase","phase":"dreaming","progress":1}'), null);
	assert.equal(parseResearchEvent('{"type":"unknown"}'), null);
});

test("the sources status reads which key-backed sources are unavailable", async () => {
	const { researchSourcesStatusSchema } = await import("@/features/notebooks/model/research");
	assert.deepEqual(
		researchSourcesStatusSchema.parse({ unavailable: { exa: "not_connected" } }).unavailable,
		{ exa: "not_connected" }
	);
	assert.deepEqual(researchSourcesStatusSchema.parse({}).unavailable, {});
	assert.throws(() => researchSourcesStatusSchema.parse({ unavailable: { exa: "broken" } }));
});
