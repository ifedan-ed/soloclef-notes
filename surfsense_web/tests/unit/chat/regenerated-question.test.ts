import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import type { ThreadMessageLike } from "@assistant-ui/react";
import {
	regeneratedQuestion,
	savedMessageId,
} from "../../../features/chat-thread/regenerated-question";

const message = (id: string, role: "user" | "assistant"): ThreadMessageLike => ({
	id,
	role,
	content: [{ type: "text", text: id }],
});

const chat = [
	message("msg-11", "user"),
	message("msg-12", "assistant"),
	message("msg-13", "user"),
	message("msg-14", "assistant"),
];

test("Regenerate asks again the question the answer belongs to", () => {
	assert.equal(regeneratedQuestion(chat, "msg-13")?.id, "msg-13");
	assert.equal(regeneratedQuestion(chat, "msg-12")?.id, "msg-11");
});

test("without a known message it is the latest question", () => {
	assert.equal(regeneratedQuestion(chat, null)?.id, "msg-13");
	assert.equal(regeneratedQuestion(chat, "msg-gone")?.id, "msg-13");
	assert.equal(regeneratedQuestion([message("msg-1", "assistant")], null), undefined);
});

test("only a saved message has an id the backend knows", () => {
	assert.equal(savedMessageId("msg-13"), 13);
	assert.equal(savedMessageId("msg-user-1760000000000"), null);
	assert.equal(savedMessageId(undefined), null);
});

test("the Regenerate button names its question to the backend", () => {
	const view = readFileSync(
		new URL("../../../features/chat-thread/chat-thread-view.tsx", import.meta.url),
		"utf8"
	);
	const onReload = view.slice(view.indexOf("const onReload = useCallback("));
	assert.ok(onReload.includes("regeneratedQuestion(messagesRef.current, parentId)"));
	assert.ok(onReload.includes("{ fromMessageId }"));
});
