import type { ConnectionHeaders } from "@/contracts/types/model-connections.types";

/**
 * The extra headers of an OpenAI-compatible connection, as the form edits
 * them: rows of a name and a value.
 *
 * The server holds the saved values sealed, as it holds the key, and answers
 * only their names (`header_names`). So, exactly as for the key
 * (./saved-key-draft), an editor never has the saved values, and what a
 * person wants done with them is one of three explicit things: keep them,
 * replace them all with the rows typed, or remove them.
 */
export interface HeaderRow {
	name: string;
	value: string;
}

/**
 * The names the server sets on every request to the endpoint itself. A row
 * naming one would either be overwritten or fight the key for the same
 * header, so it is refused here, before the server refuses it too.
 */
const RESERVED = new Set(["authorization", "content-type", "accept", "host", "content-length"]);

/** A header name is an HTTP token: no spaces, colons or other separators. */
const TOKEN = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/;

export type HeadersRead = { headers: ConnectionHeaders; problem: null } | { problem: string };

/**
 * The headers the rows describe, or the one sentence saying what is wrong
 * with them. A row left wholly empty is ignored (the form always offers one
 * blank row to type into); a row with only one half filled in is a mistake
 * and is said, not dropped.
 */
export function headersFromRows(rows: readonly HeaderRow[]): HeadersRead {
	const headers: ConnectionHeaders = {};
	const seen = new Set<string>();
	for (const row of rows) {
		const name = row.name.trim();
		const value = row.value.trim();
		if (!name && !value) continue;
		if (!name || !value) return { problem: "Each header needs both a name and a value." };
		if (!TOKEN.test(name)) {
			return { problem: `${name} is not a valid header name.` };
		}
		const lowered = name.toLowerCase();
		if (RESERVED.has(lowered)) {
			return { problem: `${name} is set by the server itself. Put a key in the API key field.` };
		}
		// Header names are not case-sensitive, so two spellings are one header.
		if (seen.has(lowered)) return { problem: `${name} is given twice.` };
		seen.add(lowered);
		headers[name] = value;
	}
	return { headers, problem: null };
}

/** What the person editing a saved connection wants done with its headers. */
export type SavedHeadersIntent =
	| { kind: "keep" }
	| { kind: "replace"; headers: ConnectionHeaders }
	| { kind: "remove" };

/**
 * The intent of an edit. Typed headers always win, as a typed key does: a
 * person who asked to remove the saved headers and then typed new ones wants
 * the new ones. Rows that describe no header mean "keep", never "send {}",
 * because an empty object would remove every saved header unasked.
 */
export function savedHeadersIntent(
	typed: ConnectionHeaders,
	removeRequested: boolean
): SavedHeadersIntent {
	if (Object.keys(typed).length > 0) return { kind: "replace", headers: typed };
	if (removeRequested) return { kind: "remove" };
	return { kind: "keep" };
}
