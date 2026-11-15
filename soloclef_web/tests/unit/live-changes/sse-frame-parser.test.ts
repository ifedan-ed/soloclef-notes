import assert from "node:assert/strict";
import { test } from "node:test";
import { SseFrameParser } from "@/features/live-changes/lib/sse-frame-parser";
import { parseStreamMessage } from "@/features/live-changes/model/live-change";

test("a frame ends at a blank line and carries its id and data", () => {
	const parser = new SseFrameParser();
	assert.deepEqual(parser.push('id: 41\ndata: {"type":"inbox"}\n\n'), [
		{ id: "41", data: '{"type":"inbox"}' },
	]);
});

test("a frame split anywhere across chunks comes out whole, once", () => {
	const parser = new SseFrameParser();
	const text =
		'data: {"type":"ready","resumed":false}\n\nid: 7\ndata: {"type":"documents","id":12}\n\n';
	const frames = [...text].flatMap((char) => parser.push(char));
	assert.deepEqual(frames, [
		{ data: '{"type":"ready","resumed":false}' },
		{ id: "7", data: '{"type":"documents","id":12}' },
	]);
});

test("CRLF line ends work, also with the CR and LF in different chunks", () => {
	const parser = new SseFrameParser();
	assert.deepEqual(parser.push("id: 1\r"), []);
	assert.deepEqual(parser.push("\ndata: x\r\n\r"), []);
	assert.deepEqual(parser.push("\n"), [{ id: "1", data: "x" }]);
});

test("comments (the keepalive) and unknown fields produce nothing", () => {
	const parser = new SseFrameParser();
	assert.deepEqual(parser.push(": keepalive\n\n"), []);
	assert.deepEqual(parser.push("event: change\nretry: 10\n\n"), []);
	assert.deepEqual(parser.push(": keepalive\ndata: y\n\n"), [{ data: "y" }]);
});

test("several data lines join with a newline; no space after the colon is fine", () => {
	const parser = new SseFrameParser();
	assert.deepEqual(parser.push("data:one\ndata: two\ndata\n\n"), [{ data: "one\ntwo\n" }]);
});

test("an id belongs to its own frame only", () => {
	const parser = new SseFrameParser();
	assert.deepEqual(parser.push("id: 9\ndata: a\n\ndata: b\n\n"), [
		{ id: "9", data: "a" },
		{ data: "b" },
	]);
});

test("the ready frame and change frames read as the contract says", () => {
	assert.deepEqual(parseStreamMessage('{"type":"ready","resumed":true}'), {
		kind: "ready",
		resumed: true,
	});
	assert.deepEqual(parseStreamMessage('{"type":"ready"}'), { kind: "ready", resumed: false });
	assert.deepEqual(parseStreamMessage('{"type":"chats","id":55,"notebook_id":7}'), {
		kind: "change",
		change: { type: "chats", id: 55, notebook_id: 7 },
	});
	assert.deepEqual(parseStreamMessage('{"type":"inbox"}'), {
		kind: "change",
		change: { type: "inbox" },
	});
});

test("ids that cannot narrow anything are dropped, not trusted", () => {
	assert.deepEqual(parseStreamMessage('{"type":"documents","id":"12","notebook_id":-1}'), {
		kind: "change",
		change: { type: "documents" },
	});
});

test("data that is not the contract's JSON is skipped, not thrown", () => {
	assert.deepEqual(parseStreamMessage("not json"), { kind: "ignored" });
	assert.deepEqual(parseStreamMessage("[1,2]"), { kind: "ignored" });
	assert.deepEqual(parseStreamMessage('{"id":3}'), { kind: "ignored" });
});
