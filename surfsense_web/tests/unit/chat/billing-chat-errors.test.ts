import assert from "node:assert/strict";
import test from "node:test";
import { classifyChatError } from "@/lib/chat/chat-error-classifier";
import { tagPreAcceptSendFailure } from "@/lib/chat/chat-request-errors";

const streamError = (errorCode: string, message = "server says no") =>
	Object.assign(new Error(message), { errorCode });

test("billing refusals on the chat stream are expected and point at billing", () => {
	for (const code of [
		"INSUFFICIENT_CREDITS",
		"PLAN_MODEL_TIER_NOT_INCLUDED",
		"PREMIUM_QUOTA_EXHAUSTED",
	]) {
		const result = classifyChatError({ error: streamError(code), flow: "new" });
		assert.equal(result.kind, "billing_refused", code);
		assert.equal(result.channel, "toast");
		assert.equal(result.isExpected, true);
		assert.equal(result.telemetryEvent, "chat_blocked");
		assert.equal(result.errorCode, code);
		assert.equal(result.userMessage, "server says no");
		assert.equal(result.billingPath, "/billing");
	}
});

test("a billing refusal without a message still says something", () => {
	const result = classifyChatError({ error: streamError("INSUFFICIENT_CREDITS", ""), flow: "new" });
	assert.ok(result.userMessage.length > 0);
});

test("billing codes survive the pre-accept send wrapper", () => {
	for (const code of ["INSUFFICIENT_CREDITS", "PLAN_MODEL_TIER_NOT_INCLUDED"]) {
		const tagged = tagPreAcceptSendFailure(streamError(code)) as { errorCode?: string };
		assert.equal(tagged.errorCode, code);
	}
	const other = tagPreAcceptSendFailure(streamError("SOMETHING_ELSE")) as { errorCode?: string };
	assert.equal(other.errorCode, "SEND_FAILED_PRE_ACCEPT");
});
