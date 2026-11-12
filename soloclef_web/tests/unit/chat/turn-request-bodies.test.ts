import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import {
	deriveMentionedPayload,
	type MentionedDocumentInfo,
} from "@/atoms/chat/mentioned-documents.atom";
import {
	newChatRequestBody,
	regenerateRequestBody,
	resumeRequestBody,
} from "@/lib/chat/stream-engine/turn-request-bodies";

// Run with: pnpm exec tsx --test tests/unit/chat/turn-request-bodies.test.ts

const CLOUD = { filesystem_mode: "cloud", client_platform: "web" } as const;

const MENTIONS: MentionedDocumentInfo[] = [
	{ id: 11, title: "Notes", document_type: "FILE", kind: "doc" },
	{ id: 4, title: "Term one", kind: "folder" },
];

/** A body as the server receives it: what JSON.stringify actually puts on the wire. */
function onTheWire(body: Record<string, unknown>): Record<string, unknown> {
	return JSON.parse(JSON.stringify(body));
}

/** No key anywhere in the body, at any depth, names a workspace. */
function assertNamesNoWorkspace(body: Record<string, unknown>) {
	assert.doesNotMatch(JSON.stringify(body), /workspace/i);
}

test("a new turn names the thread and the question, and no owner", () => {
	const body = onTheWire(
		newChatRequestBody({
			threadId: 42,
			userQuery: "  What is osmosis?  ",
			turnToolFields: { retrieval_scope: "documents" },
			selection: CLOUD,
			messageHistory: [{ role: "user", content: "Hello" }],
			mentionPayload: deriveMentionedPayload(MENTIONS),
			mentionedDocuments: MENTIONS,
			disabledTools: ["web_search"],
			userImages: [],
		})
	);

	assertNamesNoWorkspace(body);
	assert.equal(body.chat_id, 42);
	// The question is trimmed; the server refuses nothing for stray spaces,
	// but the stored message should not carry them.
	assert.equal(body.user_query, "What is osmosis?");
	assert.equal(body.retrieval_scope, "documents");
	assert.deepEqual(body.mentioned_document_ids, [11]);
	assert.deepEqual(body.mentioned_folder_ids, [4]);
	assert.deepEqual(body.mentioned_documents, MENTIONS);
	assert.deepEqual(body.disabled_tools, ["web_search"]);
});

test("a new turn leaves out what it has none of, and never sends an empty list", () => {
	const body = onTheWire(
		newChatRequestBody({
			threadId: 1,
			userQuery: "Hi",
			turnToolFields: { retrieval_scope: "all" },
			selection: CLOUD,
			messageHistory: [],
			mentionPayload: deriveMentionedPayload([]),
			mentionedDocuments: [],
			disabledTools: [],
			userImages: [],
		})
	);

	for (const absent of [
		"preferred_tool",
		"mentioned_document_ids",
		"mentioned_folder_ids",
		"mentioned_connector_ids",
		"mentioned_connectors",
		"mentioned_thread_ids",
		"mentioned_documents",
		"disabled_tools",
		"user_images",
	]) {
		assert.equal(absent in body, false, `${absent} should be absent`);
	}
});

test("a new turn carries its pictures and the tool that was picked", () => {
	const image = { media_type: "image/png", data: "AAAA" } as const;
	const body = onTheWire(
		newChatRequestBody({
			threadId: 1,
			userQuery: "Describe this",
			turnToolFields: { retrieval_scope: "all", preferred_tool: "pubmed" },
			selection: CLOUD,
			messageHistory: [],
			mentionPayload: deriveMentionedPayload([]),
			mentionedDocuments: [],
			disabledTools: [],
			userImages: [image],
		})
	);
	assert.deepEqual(body.user_images, [image]);
	assert.equal(body.preferred_tool, "pubmed");
});

test("a resume sends the decisions and no owner: the path names the thread", () => {
	const decisions = [{ type: "approve" as const }, { type: "reject" as const, message: "No" }];
	const body = onTheWire(
		resumeRequestBody({
			retrievalScope: "documents",
			decisions,
			disabledTools: [],
			selection: CLOUD,
		})
	);

	assertNamesNoWorkspace(body);
	assert.deepEqual(body, {
		retrieval_scope: "documents",
		decisions,
		filesystem_mode: "cloud",
		client_platform: "web",
	});
});

test("a regenerate sends a null question, the old mentions and no owner", () => {
	const body = onTheWire(
		regenerateRequestBody({
			retrievalScope: "documents",
			userQuery: null,
			disabledTools: [],
			selection: CLOUD,
			mentions: MENTIONS,
		})
	);

	assertNamesNoWorkspace(body);
	// null, not absent: it is how the server tells "answer the same question
	// again" from an edit.
	assert.equal(body.user_query, null);
	assert.deepEqual(body.mentioned_document_ids, [11]);
	assert.deepEqual(body.mentioned_folder_ids, [4]);
	// A plain regenerate keeps the message's pictures by not mentioning them.
	assert.equal("user_images" in body, false);
	assert.equal("from_message_id" in body, false);
	assert.equal("revert_actions" in body, false);
});

test("an edit sends its pictures even when it removed them all, and where to rewind to", () => {
	const body = onTheWire(
		regenerateRequestBody({
			retrievalScope: "all",
			userQuery: "A better question",
			disabledTools: ["web_search"],
			selection: CLOUD,
			mentions: [],
			editedUserImages: [],
			fromMessageId: 7,
			revertActions: true,
		})
	);

	assertNamesNoWorkspace(body);
	assert.equal(body.user_query, "A better question");
	// The empty list is the message: "this edit has no pictures".
	assert.deepEqual(body.user_images, []);
	// A forced tool belongs to the one new message it was picked for.
	assert.equal("preferred_tool" in body, false);
	assert.equal(body.from_message_id, 7);
	assert.equal(body.revert_actions, true);
	assert.deepEqual(body.disabled_tools, ["web_search"]);
});

test("reverting actions is asked for only together with a message to rewind to", () => {
	const body = onTheWire(
		regenerateRequestBody({
			retrievalScope: "all",
			userQuery: "Again",
			disabledTools: [],
			selection: CLOUD,
			mentions: [],
			revertActions: true,
		})
	);
	assert.equal("revert_actions" in body, false);
});

test("the engine builds every turn body here and writes none by hand", () => {
	// A body assembled inline in the engine is one this file cannot see, and
	// is where a workspace id would come back unnoticed.
	const engine = readFileSync(
		new URL("../../../lib/chat/stream-engine/engine.ts", import.meta.url),
		"utf8"
	);
	assert.doesNotMatch(engine, /workspace_id/);
	for (const builder of ["newChatRequestBody(", "resumeRequestBody(", "regenerateRequestBody("]) {
		assert.ok(engine.includes(builder), `engine.ts should call ${builder}...)`);
	}
});
