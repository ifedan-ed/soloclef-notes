/**
 * @jest-environment node
 */
import { ApiClient, type TokenPair } from "../../api/client";
import { ApiError } from "../../api/errors";
import { CitationNumbering, parseCitations } from "../citations";
import { activeMentionQuery, buildChatRequest, mentionFromSource, stripActiveMention } from "../request";
import { streamChatTurn } from "../send";
import { SseParser, readSseStream } from "../sse";
import { AssistantMessageBuilder, StreamError, answerText, mentionsFromStored, partsFromStored } from "../stream-state";

const encoder = new TextEncoder();

function streamOf(chunks: (string | Uint8Array)[]): ReadableStream<Uint8Array> {
	return new ReadableStream({
		start(controller) {
			for (const chunk of chunks) controller.enqueue(typeof chunk === "string" ? encoder.encode(chunk) : chunk);
			controller.close();
		},
	});
}

const sse = (payload: unknown) => `data: ${JSON.stringify(payload)}\n\n`;

describe("SseParser", () => {
	it("splits on blank lines and keeps partial events buffered", () => {
		const parser = new SseParser();
		expect(parser.push('data: {"type":"text-delta","delta":"He')).toEqual([]);
		expect(parser.push('llo"}\n\ndata: {"type":"finish"}\n')).toEqual([{ type: "text-delta", delta: "Hello" }]);
		expect(parser.push("\n")).toEqual([{ type: "finish" }]);
	});

	it("handles CRLF, [DONE], comments and malformed JSON like the web reader", () => {
		const parser = new SseParser();
		const events = parser.push(
			': keep-alive\r\n\r\ndata: {"type":"start"}\r\n\r\ndata: [DONE]\r\n\r\ndata: {not json}\r\n\r\nevent: x\r\ndata: {"type":"finish"}\r\n\r\n'
		);
		expect(events).toEqual([{ type: "start" }, { type: "finish" }]);
	});

	it("flushes a last event that lacks its trailing blank line", () => {
		const parser = new SseParser();
		parser.push('data: {"type":"finish"}');
		expect(parser.end()).toEqual([{ type: "finish" }]);
	});

	it("decodes multi-byte characters split across chunks", async () => {
		const bytes = encoder.encode(sse({ type: "text-delta", delta: "naïve 🧠 ünïcode" }));
		const split = bytes.indexOf(0xf0) + 2; // inside the emoji
		const events: unknown[] = [];
		await readSseStream(streamOf([bytes.slice(0, split), bytes.slice(split)]), (e) => events.push(e));
		expect(events).toEqual([{ type: "text-delta", delta: "naïve 🧠 ünïcode" }]);
	});
});

describe("AssistantMessageBuilder", () => {
	it("follows the web's part rules for text, reasoning and tools", () => {
		const builder = new AssistantMessageBuilder();
		const events = [
			{ type: "start" },
			{ type: "data-turn-info", data: { chat_turn_id: "t1" } },
			{ type: "reasoning-start", id: "r1" },
			{ type: "reasoning-delta", delta: "Looking at " },
			{ type: "reasoning-delta", delta: "sources" },
			{ type: "reasoning-end", id: "r1" },
			{ type: "tool-input-start", toolCallId: "c1", toolName: "search_knowledge_base" },
			{ type: "tool-input-delta", toolCallId: "c1", inputTextDelta: '{"q":' },
			{ type: "tool-input-available", toolCallId: "c1", toolName: "search_knowledge_base", input: { q: "asthma" } },
			{ type: "tool-output-available", toolCallId: "c1", output: { hits: 3 } },
			{ type: "text-delta", delta: "Asthma is " },
			{ type: "text-delta", delta: "common [citation:12]." },
			{ type: "finish" },
		];
		for (const e of events) builder.apply(e);
		expect(builder.finished).toBe(true);
		expect(builder.snapshot()).toEqual([
			{ type: "reasoning", text: "Looking at sources", status: "completed" },
			{
				type: "tool-call",
				toolCallId: "c1",
				toolName: "search_knowledge_base",
				args: { q: "asthma" },
				argsText: '{"q":',
				result: { hits: 3 },
			},
			{ type: "text", text: "Asthma is common [citation:12]." },
		]);
	});

	it("opens a new text part after a tool call", () => {
		const builder = new AssistantMessageBuilder();
		builder.apply({ type: "text-delta", delta: "Before" });
		builder.apply({ type: "tool-input-start", toolCallId: "c1", toolName: "t" });
		builder.apply({ type: "text-delta", delta: "After" });
		expect(builder.snapshot().map((p) => p.type)).toEqual(["text", "tool-call", "text"]);
		expect(answerText(builder.snapshot())).toBe("Before\n\nAfter");
	});

	it("reports side effects and throws on error events", () => {
		const effects = {
			onUserMessageId: jest.fn(),
			onAssistantMessageId: jest.fn(),
			onThreadTitle: jest.fn(),
			onTruncated: jest.fn(),
		};
		const builder = new AssistantMessageBuilder(effects);
		builder.apply({ type: "data-user-message-id", data: { message_id: 10, turn_id: "t" } });
		builder.apply({ type: "data-assistant-message-id", data: { message_id: 11, turn_id: "t" } });
		builder.apply({ type: "data-thread-title-update", data: { threadId: 5, title: "Asthma basics" } });
		builder.apply({ type: "data-token-usage", data: { truncated: true } });
		expect(effects.onUserMessageId).toHaveBeenCalledWith(10);
		expect(effects.onAssistantMessageId).toHaveBeenCalledWith(11);
		expect(effects.onThreadTitle).toHaveBeenCalledWith(5, "Asthma basics");
		expect(effects.onTruncated).toHaveBeenCalled();
		expect(() =>
			builder.apply({ type: "error", message: "Out of credits", errorCode: "INSUFFICIENT_CREDITS" })
		).toThrow(StreamError);
	});

	it("ignores unknown events", () => {
		const builder = new AssistantMessageBuilder();
		expect(builder.apply({ type: "data-activity", data: {} })).toBe(false);
		expect(builder.apply("garbage")).toBe(false);
		expect(builder.snapshot()).toEqual([]);
	});
});

describe("stored messages", () => {
	it("reads persisted parts and mention chips", () => {
		const content = [
			{ type: "mentioned-documents", documents: [{ id: 3, title: "GINA 2024", kind: "doc" }] },
			{ type: "text", text: "What changed?" },
			{ type: "data-activities", data: {} },
		];
		expect(partsFromStored(content)).toEqual([{ type: "text", text: "What changed?" }]);
		expect(mentionsFromStored(content)).toEqual(["GINA 2024"]);
		expect(partsFromStored("legacy plain text")).toEqual([{ type: "text", text: "legacy plain text" }]);
	});
});

describe("citations", () => {
	it("parses the web's citation forms", () => {
		expect(parseCitations("A [citation:12] B [citation:3, doc-4] C 【citation:https://x.org/a】")).toEqual([
			"A ",
			{ kind: "chunk", chunkId: 12, isDocsChunk: false },
			" B ",
			{ kind: "chunk", chunkId: 3, isDocsChunk: false },
			{ kind: "chunk", chunkId: 4, isDocsChunk: true },
			" C ",
			{ kind: "url", url: "https://x.org/a" },
		]);
		expect(parseCitations("no citations")).toEqual(["no citations"]);
	});

	it("numbers citations by first appearance", () => {
		const numbering = new CitationNumbering();
		const tokens = parseCitations("[citation:9][citation:4][citation:9][citation:-1]").filter(
			(s): s is Exclude<typeof s, string> => typeof s !== "string"
		);
		expect(tokens.map((t) => numbering.numberOf(t))).toEqual([1, 2, 1, null]);
	});
});

describe("chat request", () => {
	const source = {
		id: 7,
		kind: "document" as const,
		target_id: 70,
		title: "GINA 2024",
		document_type: null,
		status: "ready",
		enabled: true,
		added_at: "2026-09-01T00:00:00Z",
	};

	it("builds the web's new_chat body with history and mentions", () => {
		const body = buildChatRequest({
			threadId: 5,
			workspaceId: 1,
			query: "  What changed?  ",
			history: [
				{ role: "user", parts: [{ type: "text", text: "Hi" }] },
				{ role: "assistant", parts: [{ type: "reasoning", text: "x", status: "completed" }] },
				{ role: "assistant", parts: [{ type: "text", text: "Hello" }] },
			],
			mentions: [
				mentionFromSource(source),
				mentionFromSource({ ...source, kind: "folder", target_id: 9, title: "Guidelines" }),
			],
		});
		expect(body).toEqual({
			chat_id: 5,
			user_query: "What changed?",
			workspace_id: 1,
			retrieval_scope: "documents",
			filesystem_mode: "cloud",
			client_platform: "web",
			messages: [
				{ role: "user", content: "Hi" },
				{ role: "assistant", content: "Hello" },
			],
			mentioned_document_ids: [70],
			mentioned_folder_ids: [9],
			mentioned_documents: [
				{ id: 70, title: "GINA 2024", kind: "doc", document_type: "FILE" },
				{ id: 9, title: "Guidelines", kind: "folder" },
			],
		});
	});

	it("tracks the @mention being typed", () => {
		expect(activeMentionQuery("Compare @gin")).toBe("gin");
		expect(activeMentionQuery("@")).toBe("");
		expect(activeMentionQuery("mail me@example.com")).toBeNull();
		expect(stripActiveMention("Compare @gin")).toBe("Compare ");
	});
});

describe("streamChatTurn", () => {
	const tokens: TokenPair = { accessToken: "a", refreshToken: "r", accessExpiresAt: Date.now() / 1000 + 3600 };
	const store = { load: async () => tokens, save: async () => {}, clear: async () => {} };

	it("streams events to onUpdate and resolves with the final answer", async () => {
		const posted: unknown[] = [];
		const fetchImpl = async (_url: string, init: RequestInit) => {
			posted.push(JSON.parse(String(init.body)));
			return new Response(
				streamOf([
					sse({ type: "start" }),
					sse({ type: "data-user-message-id", data: { message_id: 1, turn_id: "t" } }),
					'data: {"type":"text-delta","delta":"Hel',
					'lo"}\n\n',
					sse({ type: "text-delta", delta: " world" }),
					sse({ type: "finish" }),
					"data: [DONE]\n\n",
				]),
				{ status: 200, headers: { "Content-Type": "text/event-stream" } }
			);
		};
		const api = new ApiClient({ baseUrl: "https://x.test", store, fetch: fetchImpl });
		const updates: string[] = [];
		const onUserMessageId = jest.fn();
		const parts = await streamChatTurn({
			api,
			body: { chat_id: 1 },
			fetchImpl,
			effects: { onUserMessageId },
			onUpdate: (p) => updates.push(answerText(p)),
			throttleMs: 0,
		});
		expect(posted).toEqual([{ chat_id: 1 }]);
		expect(answerText(parts)).toBe("Hello world");
		expect(updates[updates.length - 1]).toBe("Hello world");
		expect(onUserMessageId).toHaveBeenCalledWith(1);
	});

	it("retries while the previous turn is cancelling, then gives up with the server's error", async () => {
		let attempts = 0;
		const fetchImpl = async () => {
			attempts += 1;
			return new Response(
				JSON.stringify({ detail: { message: "Still stopping", errorCode: "TURN_CANCELLING", retry_after_ms: 1 } }),
				{ status: 409 }
			);
		};
		const api = new ApiClient({ baseUrl: "https://x.test", store, fetch: fetchImpl });
		const error = await streamChatTurn({
			api,
			body: {},
			fetchImpl,
			onUpdate: () => {},
			sleep: async () => {},
		}).catch((e) => e);
		expect(attempts).toBe(4);
		expect(error).toBeInstanceOf(ApiError);
		expect(error).toMatchObject({ status: 409, code: "TURN_CANCELLING", message: "Still stopping" });
	});

	it("rejects with StreamError when the server ends the turn with an error", async () => {
		const fetchImpl = async () =>
			new Response(streamOf([sse({ type: "text-delta", delta: "Part" }), sse({ type: "error", message: "boom" })]), {
				status: 200,
			});
		const api = new ApiClient({ baseUrl: "https://x.test", store, fetch: fetchImpl });
		const seen: string[] = [];
		await expect(
			streamChatTurn({ api, body: {}, fetchImpl, onUpdate: (p) => seen.push(answerText(p)), throttleMs: 0 })
		).rejects.toThrow("boom");
		expect(seen[seen.length - 1]).toBe("Part");
	});
});
