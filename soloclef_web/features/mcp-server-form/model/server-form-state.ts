import {
	type MCPAuthRequest,
	type MCPServerConfig,
	type MCPTransport,
	mcpServerConfigRead,
} from "@/contracts/types/mcp.types";

/**
 * The "Add MCP server" form's state, and the two conversions it needs: a
 * saved server's masked answer into the form, and the form into the body the
 * API reads. Pure, so the rules a person meets at the form are tested
 * without rendering it.
 */

/** How the form signs in to the server. One choice, one set of fields. */
export type AuthChoice = MCPAuthRequest["type"];

/** One extra header row. `id` keys the row in the list; it is never sent. */
export interface HeaderRow {
	id: string;
	name: string;
	value: string;
}

export interface ServerFormState {
	name: string;
	url: string;
	transport: MCPTransport;
	auth: AuthChoice;
	/** "API key in a header": which header, and the key. */
	headerName: string;
	headerValue: string;
	bearerToken: string;
	/** OAuth, when the authorization server cannot register this app itself. */
	clientId: string;
	clientSecret: string;
	extraHeaders: HeaderRow[];
}

/** What a masked secret starts with in every answer. */
export const MASK = "••••";

/** Whether a value is one the server masked, which stands for the stored secret. */
export function isMasked(value: string): boolean {
	return value.startsWith(MASK);
}

/** The header name most API-key servers use, offered as the field's starting value. */
export const DEFAULT_KEY_HEADER = "X-API-Key";

let rowCounter = 0;
/** A fresh row id: unique within the page, which is all a list key needs. */
export function newRowId(): string {
	rowCounter += 1;
	return `header-${rowCounter}`;
}

export function emptyServerForm(): ServerFormState {
	return {
		name: "",
		url: "",
		transport: "streamable-http",
		auth: "none",
		headerName: DEFAULT_KEY_HEADER,
		headerValue: "",
		bearerToken: "",
		clientId: "",
		clientSecret: "",
		extraHeaders: [],
	};
}

/** What the form learns of a saved OAuth server, beside the fields. */
export interface SavedOAuthState {
	authorized: boolean;
	dynamicRegistration: boolean;
	authorizationServer: string | null;
}

export interface SavedServerForm {
	state: ServerFormState;
	/** Set when the saved server signs in with OAuth. */
	oauth: SavedOAuthState | null;
}

/**
 * A saved server's answer as the form's state. Secrets arrive masked and
 * stay masked in the fields: saved unchanged, they keep the stored ones.
 *
 * An answer that is not the API's shape is null, not a half-filled form: a
 * form guessed from an unknown shape would save something nobody typed.
 */
export function serverFormFromAnswer(name: string, serverConfig: unknown): SavedServerForm | null {
	const parsed = mcpServerConfigRead.safeParse(serverConfig);
	if (!parsed.success) return null;
	const config = parsed.data;
	const state: ServerFormState = {
		...emptyServerForm(),
		name,
		url: config.url,
		transport: config.transport,
		auth: config.auth.type,
		extraHeaders: Object.entries(config.headers).map(([headerName, value]) => ({
			id: newRowId(),
			name: headerName,
			value,
		})),
	};
	let oauth: SavedOAuthState | null = null;
	switch (config.auth.type) {
		case "header":
			state.headerName = config.auth.header_name;
			state.headerValue = config.auth.value;
			break;
		case "bearer":
			state.bearerToken = config.auth.token;
			break;
		case "oauth":
			state.clientId = config.auth.client_id ?? "";
			state.clientSecret = config.auth.client_secret ?? "";
			oauth = {
				authorized: config.auth.authorized,
				dynamicRegistration: config.auth.dynamic_registration,
				authorizationServer: config.auth.authorization_server,
			};
			break;
		case "none":
			break;
	}
	return { state, oauth };
}

/** The form as the body's `server_config`. Only the chosen sign-in's fields are sent. */
export function serverConfigFromForm(state: ServerFormState): MCPServerConfig {
	const headers: Record<string, string> = {};
	for (const row of state.extraHeaders) {
		// A row left wholly blank is an unfilled row, not a header.
		if (row.name.trim() === "" && row.value === "") continue;
		headers[row.name.trim()] = row.value;
	}
	return { url: state.url.trim(), transport: state.transport, auth: authFromForm(state), headers };
}

function authFromForm(state: ServerFormState): MCPAuthRequest {
	switch (state.auth) {
		case "none":
			return { type: "none" };
		case "header":
			return { type: "header", header_name: state.headerName.trim(), value: state.headerValue };
		case "bearer":
			return { type: "bearer", token: state.bearerToken.trim() };
		case "oauth": {
			const auth: Extract<MCPAuthRequest, { type: "oauth" }> = { type: "oauth" };
			// Sent only when typed: with dynamic registration there is none.
			if (state.clientId.trim()) auth.client_id = state.clientId.trim();
			if (state.clientSecret) auth.client_secret = state.clientSecret;
			return auth;
		}
	}
}

/** The name column's width on the server. */
export const NAME_LIMIT = 100;

/** RFC 9110's token: the characters a header name may hold. */
const HEADER_NAME = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/;

/** Where a field's message is shown: a fixed field, or an extra header row's name or value. */
export type FieldKey =
	| "name"
	| "url"
	| "headerName"
	| "headerValue"
	| "bearerToken"
	| "clientId"
	| "oauth"
	| `row:${string}:name`
	| `row:${string}:value`;

export type FieldErrors = Partial<Record<FieldKey, string>>;

/**
 * Every problem the server would refuse, said at the field that has it.
 *
 * The two OAuth inputs are discovery's answer: `needsClientId` when the
 * authorization server offers no dynamic registration, so a client id must
 * be typed; `oauthProblem` when the server offers no usable sign-in at all
 * (or the answer is not in yet), which no field can fix.
 */
export function validateServerForm(
	state: ServerFormState,
	{ needsClientId, oauthProblem = null }: { needsClientId: boolean; oauthProblem?: string | null }
): FieldErrors {
	const errors: FieldErrors = {};
	const name = state.name.trim();
	if (!name) errors.name = "Give the server a name";
	else if ([...name].length > NAME_LIMIT)
		errors.name = `A name is at most ${NAME_LIMIT} characters`;

	const urlProblem = serverUrlProblem(state.url);
	if (urlProblem) errors.url = urlProblem;

	// The header the sign-in itself fills, so an extra row cannot repeat it.
	let authHeader: string | null = null;
	switch (state.auth) {
		case "header": {
			const headerName = state.headerName.trim();
			if (!headerName) errors.headerName = "Name the header the key goes in";
			else if (!HEADER_NAME.test(headerName))
				errors.headerName = "A header name has letters, digits and - only";
			if (!state.headerValue) errors.headerValue = "Enter the API key";
			authHeader = headerName.toLowerCase();
			break;
		}
		case "bearer":
			if (!state.bearerToken.trim()) errors.bearerToken = "Enter the token";
			authHeader = "authorization";
			break;
		case "oauth":
			if (oauthProblem) errors.oauth = oauthProblem;
			if (needsClientId && !state.clientId.trim())
				errors.clientId = "This server's sign-in needs a client ID";
			authHeader = "authorization";
			break;
		case "none":
			break;
	}

	const seen = new Set<string>();
	for (const row of state.extraHeaders) {
		const rowName = row.name.trim();
		if (rowName === "" && row.value === "") continue;
		const lowered = rowName.toLowerCase();
		if (!rowName) errors[`row:${row.id}:name`] = "Name the header";
		else if (!HEADER_NAME.test(rowName))
			errors[`row:${row.id}:name`] = "A header name has letters, digits and - only";
		else if (lowered === authHeader)
			errors[`row:${row.id}:name`] =
				lowered === "authorization"
					? "The sign-in already sets Authorization"
					: "The API key already goes in this header";
		else if (seen.has(lowered)) errors[`row:${row.id}:name`] = "This header is already listed";
		if (!row.value) errors[`row:${row.id}:value`] = "Enter the header's value";
		seen.add(lowered);
	}
	return errors;
}

/** Why an address cannot be a server's, or null. The server checks where it leads. */
export function serverUrlProblem(raw: string): string | null {
	const text = raw.trim();
	if (!text) return "Enter the server's URL";
	let url: URL;
	try {
		url = new URL(text);
	} catch {
		return "This is not a full URL (https://…)";
	}
	if (url.protocol !== "https:") return "The server URL must start with https://";
	if (!url.hostname) return "The URL has no host";
	if (url.username || url.password) return "Put credentials in a header, not in the URL";
	return null;
}

/** Whether the URL is complete enough to ask the server about OAuth. */
export function canDiscover(url: string): boolean {
	return serverUrlProblem(url) === null;
}

export function hasErrors(errors: FieldErrors): boolean {
	return Object.keys(errors).length > 0;
}
