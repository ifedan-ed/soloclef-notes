import assert from "node:assert/strict";
import test from "node:test";

import { classifyChatError } from "@/lib/chat/chat-error-classifier";
import { toHttpResponseError } from "@/lib/chat/chat-request-errors";

const BUSY_MESSAGE =
	"A reply is still being written in this chat, maybe in another tab or on another device. " +
	"Wait for it to finish, or press Stop to end it, then try again.";

test("a 409 while another page's turn runs tells the person what to do", async () => {
	const response = new Response(
		JSON.stringify({ detail: { errorCode: "THREAD_BUSY", message: BUSY_MESSAGE } }),
		{ status: 409, headers: { "Content-Type": "application/json" } }
	);
	const error = await toHttpResponseError(response);
	const normalized = classifyChatError({ error, flow: "regenerate" });

	assert.equal(normalized.kind, "thread_busy");
	assert.equal(normalized.channel, "toast");
	assert.equal(normalized.userMessage, BUSY_MESSAGE);
});
