/**
 * What the API's change stream (`GET /api/v1/events`) says, as the app reads
 * it. The stream carries no row contents: a change only names what moved, and
 * the app reads the rows again over REST (lib/live-query-targets.ts).
 */

/** A change of one kind of the account's data; `id` and `notebook_id` only narrow it. */
export interface LiveChange {
	type: string;
	id?: number;
	notebook_id?: number;
}

/** One frame's meaning: the connection's first frame, a change, or something to skip. */
export type StreamMessage =
	| { kind: "ready"; resumed: boolean }
	| { kind: "change"; change: LiveChange }
	| { kind: "ignored" };

const IGNORED: StreamMessage = { kind: "ignored" };

/** A positive whole number, or nothing: a field the app cannot use must not narrow a refetch. */
function optionalId(value: unknown): number | undefined {
	return typeof value === "number" && Number.isInteger(value) && value > 0 ? value : undefined;
}

/**
 * Reads one frame's `data`. Anything that is not the contract's JSON is
 * skipped rather than thrown: one bad frame must not end a stream that will
 * carry good ones after it.
 */
export function parseStreamMessage(data: string): StreamMessage {
	let parsed: unknown;
	try {
		parsed = JSON.parse(data);
	} catch {
		return IGNORED;
	}
	if (typeof parsed !== "object" || parsed === null) return IGNORED;
	const record = parsed as Record<string, unknown>;
	if (typeof record.type !== "string" || record.type === "") return IGNORED;
	if (record.type === "ready") {
		return { kind: "ready", resumed: record.resumed === true };
	}
	const change: LiveChange = { type: record.type };
	const id = optionalId(record.id);
	const notebookId = optionalId(record.notebook_id);
	if (id !== undefined) change.id = id;
	if (notebookId !== undefined) change.notebook_id = notebookId;
	return { kind: "change", change };
}
