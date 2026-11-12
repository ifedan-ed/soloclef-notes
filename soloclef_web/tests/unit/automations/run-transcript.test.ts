import assert from "node:assert/strict";
import test from "node:test";

import { accountModelLine, withdrawnSentence } from "@/lib/automations/automation-model";
import { describeRunError } from "@/lib/automations/run-outcome";
import {
	countToolCalls,
	modelLine,
	readableArgs,
	readRefused,
	readRunEmail,
	readSavedNote,
	readTranscript,
	runEmailSentence,
	transcriptForDisplay,
} from "@/lib/automations/run-transcript";

const result = {
	final_message: "Three documents arrived this week.",
	model: "claude-sonnet-4-5",
	usage: { prompt_tokens: 1200, completion_tokens: 34, total_tokens: 1234 },
	refused: [{ tool: "read_web_page", reason: "It asks a person every time it is used." }],
	saved_note: { document_id: 88, notebook_id: 7, title: "Weekly digest" },
	transcript: [
		{
			role: "assistant",
			content: "",
			tool_calls: [{ name: "search_knowledge_base", args: '{"query":"this week"}' }],
		},
		{ role: "tool", tool: "search_knowledge_base", content: "[1] Budget notes" },
		{ role: "system", content: "never shown" },
		{ role: "assistant", content: "Three documents arrived this week." },
	],
};

test("a transcript is read in order, with each call and the tool it was answered by", () => {
	const entries = readTranscript(result);
	assert.deepEqual(
		entries.map((entry) => entry.role),
		["assistant", "tool", "assistant"]
	);
	assert.deepEqual(entries[0].toolCalls, [
		{ name: "search_knowledge_base", args: '{"query":"this week"}' },
	]);
	assert.equal(entries[1].tool, "search_knowledge_base");
	assert.equal(countToolCalls(entries), 1);
});

test("the final message is not listed a second time in the transcript", () => {
	const shown = transcriptForDisplay(readTranscript(result), result.final_message);
	assert.equal(shown.length, 2);
	assert.equal(shown.at(-1)?.role, "tool");
	// A last message that differs from the final one stays.
	assert.equal(transcriptForDisplay(readTranscript(result), "Something else").length, 3);
});

test("a row from an older worker, with no transcript or refusals, reads as none", () => {
	for (const older of [undefined, null, {}, { transcript: "x", refused: 3, saved_note: [] }]) {
		assert.deepEqual(readTranscript(older), []);
		assert.deepEqual(readRefused(older), []);
		assert.equal(readSavedNote(older), null);
		assert.equal(modelLine(older), null);
	}
});

test("refused tools carry their reasons, and the saved note its document", () => {
	assert.deepEqual(readRefused(result), [
		{ tool: "read_web_page", reason: "It asks a person every time it is used." },
	]);
	assert.deepEqual(readSavedNote(result), {
		documentId: 88,
		notebookId: 7,
		title: "Weekly digest",
	});
	assert.equal(readSavedNote({ saved_note: { document_id: 0, title: "x" } }), null);
});

test("the model line names the model and what it used", () => {
	assert.equal(modelLine(result), "claude-sonnet-4-5 · 1,234 tokens");
	assert.equal(modelLine({ model: "gpt-5" }), "gpt-5");
});

test("whole JSON arguments are indented; clipped ones are shown as sent", () => {
	assert.equal(readableArgs('{"query":"this week"}'), '{\n  "query": "this week"\n}');
	assert.equal(readableArgs('{"query":"this we\n… (40 more'), '{"query":"this we\n… (40 more');
	assert.equal(readableArgs("{}"), "");
});

test("the email outcome is one sentence, and absent when the run was not asked to email", () => {
	assert.equal(readRunEmail({ final_message: "x" }), null);
	assert.equal(readRunEmail({ email: { status: "queued" } }), null);
	const sent = readRunEmail({ email: { status: "sent", to: "a@example.com", reason: null } });
	assert.ok(sent);
	assert.equal(runEmailSentence(sent), "The result was emailed to a@example.com.");
	// Until the server has sent it, the page says it is on its way, not that it arrived.
	for (const status of ["pending", "sending"]) {
		const waiting = readRunEmail({ email: { status } });
		assert.ok(waiting);
		assert.equal(runEmailSentence(waiting), "The result is being emailed to you.");
	}
	const failed = readRunEmail({
		email: { status: "not_sent", to: null, reason: "The account has no verified address." },
	});
	assert.ok(failed);
	assert.equal(
		runEmailSentence(failed),
		"The result was not emailed: The account has no verified address."
	);
});

test("the worker's own error words have titles", () => {
	assert.deepEqual(describeRunError("approval_refused"), {
		title: "Needs your attention",
		tone: "warning",
	});
	assert.equal(describeRunError("model_unavailable").tone, "destructive");
	assert.equal(describeRunError("cancelled").tone, "muted");
});

const chatModel = {
	selected: null,
	model: "gpt-5",
	available: true,
	default: "gpt-5",
	models: [
		{ id: "gpt-5", label: "gpt-5", own: false },
		{ id: "claude-sonnet-4-5", label: "claude-sonnet-4-5", own: false },
		{ id: "own:12", label: "my-model", own: true, connection: "api.example.test" },
	],
};

test("no model chosen names the account's chat model as it stands", () => {
	assert.equal(accountModelLine(chatModel), "Currently gpt-5");
	assert.equal(
		accountModelLine({ ...chatModel, available: false }),
		"Currently gpt-5, which is no longer offered"
	);
	assert.match(accountModelLine(undefined), /when the run starts/);
});

test("a chosen model the proxy stopped listing is said, never swapped", () => {
	assert.equal(withdrawnSentence(chatModel, "claude-sonnet-4-5"), null);
	assert.equal(withdrawnSentence(chatModel, null), null);
	assert.equal(
		withdrawnSentence(chatModel, "old-model"),
		"old-model is no longer offered. Choose another model."
	);
	// The list not read yet is not a reason to call a model withdrawn.
	assert.equal(withdrawnSentence(undefined, "old-model"), null);
});

test("an automation on an own model is named by its label, listed or gone", () => {
	assert.equal(withdrawnSentence(chatModel, "own:12"), null);
	assert.equal(
		withdrawnSentence(chatModel, "own:99"),
		"The model from your own connection is no longer offered. Choose another model."
	);
	assert.equal(
		accountModelLine({ ...chatModel, selected: "own:12", model: "own:12" }),
		"Currently my-model"
	);
});

test("the last thing said is not listed twice when its labels became source markers", () => {
	const entries = readTranscript({
		transcript: [
			{ role: "assistant", tool_calls: [{ name: "search_knowledge_base", args: "{}" }] },
			{ role: "tool", tool: "search_knowledge_base", content: "[1] a passage" },
			{ role: "assistant", content: "Mitosis has four phases [1][7]." },
		],
	});
	// What the run saved: label 1 as its marker, label 7 (no such source) removed.
	const shown = transcriptForDisplay(entries, "Mitosis has four phases [citation:481].");
	assert.equal(shown.length, 2);
	// A different last message is kept.
	assert.equal(transcriptForDisplay(entries, "Something else.").length, 3);
});
