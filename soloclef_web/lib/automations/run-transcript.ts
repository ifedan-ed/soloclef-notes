/**
 * What one step of a run did, read out of ``step_results[i].result``, and
 * what the run did with its result afterwards (``output.email``).
 *
 * The run row is JSON the worker wrote; the contract keeps it as opaque
 * records. Everything here reads it defensively, so a row written by an
 * older worker (no transcript, no refusals) shows less and never throws.
 */

export interface TranscriptCall {
	name: string;
	/** The arguments as the model sent them: JSON text, possibly clipped. */
	args: string;
}

export interface TranscriptEntry {
	role: "assistant" | "tool" | "user";
	content: string;
	/** The tools an assistant message asked for. */
	toolCalls: TranscriptCall[];
	/** The tool a ``tool`` message is the answer of. */
	tool: string | null;
}

export interface RefusedTool {
	tool: string;
	reason: string;
}

export interface SavedNote {
	documentId: number;
	notebookId: number | null;
	title: string;
}

/**
 * What became of emailing a run's result (``output.email``). The worker ends
 * the run with ``pending``; the API, which holds the mail server, marks it
 * ``sending`` as it takes it and then ``sent`` or ``not_sent``.
 */
export interface RunEmail {
	status: "pending" | "sending" | "sent" | "not_sent";
	to: string | null;
	reason: string | null;
}

function record(value: unknown): Record<string, unknown> {
	return value && typeof value === "object" && !Array.isArray(value)
		? (value as Record<string, unknown>)
		: {};
}

function text(value: unknown): string {
	return typeof value === "string" ? value : "";
}

/** The step's conversation after the task, in order. Unknown roles are left out. */
export function readTranscript(result: unknown): TranscriptEntry[] {
	const raw = record(result).transcript;
	if (!Array.isArray(raw)) return [];
	const entries: TranscriptEntry[] = [];
	for (const item of raw) {
		const entry = record(item);
		const role = entry.role;
		if (role !== "assistant" && role !== "tool" && role !== "user") continue;
		const calls = Array.isArray(entry.tool_calls) ? entry.tool_calls : [];
		entries.push({
			role,
			content: text(entry.content),
			toolCalls: calls
				.map((call) => ({ name: text(record(call).name), args: text(record(call).args) }))
				.filter((call) => call.name !== ""),
			tool: text(entry.tool) || null,
		});
	}
	return entries;
}

/**
 * Text with its source labels taken out. The transcript holds what the model
 * wrote (``[3]``) and ``final_message`` the same words with each label turned
 * into the marker a source chip is drawn from (``[citation:481]``), or
 * removed when it named no source, so the two are compared without either.
 */
function withoutSourceLabels(text: string): string {
	return text
		.replace(/\[citation:[^\]]*\]/g, "")
		.replace(/\[\s*\d+\s*\]/g, "")
		.replace(/\s+/g, " ")
		.trim();
}

/**
 * The transcript as the step card lists it. The last thing the assistant
 * said is the step's ``final_message``, which the card already shows in
 * full above, so that one entry is dropped here instead of being read twice.
 */
export function transcriptForDisplay(
	entries: TranscriptEntry[],
	finalMessage: string | null
): TranscriptEntry[] {
	const last = entries.at(-1);
	if (
		finalMessage !== null &&
		last?.role === "assistant" &&
		last.toolCalls.length === 0 &&
		withoutSourceLabels(last.content) === withoutSourceLabels(finalMessage)
	) {
		return entries.slice(0, -1);
	}
	return entries;
}

/** The tool calls nobody was present to approve, with why each was not run. */
export function readRefused(result: unknown): RefusedTool[] {
	const raw = record(result).refused;
	if (!Array.isArray(raw)) return [];
	return raw
		.map((item) => ({ tool: text(record(item).tool), reason: text(record(item).reason) }))
		.filter((refusal) => refusal.tool !== "");
}

/** The note the step's result was saved as, when the task asked for one. */
export function readSavedNote(result: unknown): SavedNote | null {
	const saved = record(record(result).saved_note);
	const documentId = saved.document_id;
	if (typeof documentId !== "number" || !Number.isSafeInteger(documentId) || documentId < 1) {
		return null;
	}
	return {
		documentId,
		notebookId: typeof saved.notebook_id === "number" ? saved.notebook_id : null,
		title: text(saved.title) || "Untitled note",
	};
}

/** What became of emailing the result, when the automation asked for it. */
export function readRunEmail(output: unknown): RunEmail | null {
	const email = record(record(output).email);
	if (
		email.status !== "pending" &&
		email.status !== "sending" &&
		email.status !== "sent" &&
		email.status !== "not_sent"
	) {
		return null;
	}
	return {
		status: email.status,
		to: text(email.to) || null,
		reason: text(email.reason) || null,
	};
}

/** The email outcome in one sentence. */
export function runEmailSentence(email: RunEmail): string {
	if (email.status === "sent") {
		return email.to ? `The result was emailed to ${email.to}.` : "The result was emailed.";
	}
	if (email.status === "pending" || email.status === "sending") {
		return "The result is being emailed to you.";
	}
	return email.reason
		? `The result was not emailed: ${email.reason}`
		: "The result was not emailed.";
}

/** The model that answered the step and what it used, e.g. ``gpt-5 · 1,234 tokens``. */
export function modelLine(result: unknown): string | null {
	const model = text(record(result).model);
	const total = record(record(result).usage).total_tokens;
	const parts: string[] = [];
	if (model) parts.push(model);
	if (typeof total === "number" && total > 0) {
		parts.push(`${total.toLocaleString("en-US")} token${total === 1 ? "" : "s"}`);
	}
	return parts.length > 0 ? parts.join(" · ") : null;
}

/**
 * A tool call's arguments for reading: indented when they are whole JSON,
 * and as they were sent when they are not (the worker clips long ones, which
 * leaves text that no longer parses).
 */
export function readableArgs(args: string): string {
	const trimmed = args.trim();
	if (trimmed === "" || trimmed === "{}") return "";
	try {
		return JSON.stringify(JSON.parse(trimmed), null, 2);
	} catch {
		return args;
	}
}

/** How many tool calls a transcript holds, for the section's heading. */
export function countToolCalls(entries: TranscriptEntry[]): number {
	return entries.reduce((count, entry) => count + entry.toolCalls.length, 0);
}
