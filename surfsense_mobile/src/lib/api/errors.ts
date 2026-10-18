/**
 * Typed failures from the SurfSense backend, mirroring the web client's
 * error classes (surfsense_web/lib/error.ts) closely enough that the same
 * backend responses produce the same kinds of error here.
 */
export class ApiError extends Error {
	constructor(
		message: string,
		readonly status: number,
		readonly code?: string,
		readonly requestId?: string
	) {
		super(message);
		this.name = "ApiError";
	}
}

/** The server said 401 and the refresh token could not mint a new session. */
export class SessionExpiredError extends ApiError {
	constructor(message = "Your session has ended. Please sign in again.") {
		super(message, 401, "SESSION_EXPIRED");
		this.name = "SessionExpiredError";
	}
}

/** The request never reached the server (offline, DNS, TLS, wrong URL). */
export class NetworkError extends Error {
	constructor(
		message = "Unable to connect to the server. Check your connection and try again.",
		readonly cause?: unknown
	) {
		super(message);
		this.name = "NetworkError";
	}
}

export class AbortedError extends Error {
	constructor() {
		super("The request was cancelled.");
		this.name = "AbortedError";
	}
}

export function isAbortError(error: unknown): boolean {
	return (
		error instanceof AbortedError ||
		(typeof error === "object" && error !== null && (error as { name?: unknown }).name === "AbortError")
	);
}

/**
 * Pull the human message out of an error body. The backend answers with the
 * structured envelope ``{error: {message, code, request_id}}`` from its global
 * handlers, or FastAPI's plain ``{detail}`` (a string, or a list of validation
 * issues) from routes that raise HTTPException directly.
 */
export function parseErrorBody(body: unknown): { message: string; code?: string; requestId?: string } {
	if (typeof body !== "object" || body === null) return { message: "" };
	const record = body as Record<string, unknown>;
	const envelope = record.error;
	if (typeof envelope === "object" && envelope !== null) {
		const e = envelope as Record<string, unknown>;
		return {
			message: typeof e.message === "string" ? e.message : "",
			code: typeof e.code === "string" ? e.code : undefined,
			requestId: typeof e.request_id === "string" ? e.request_id : undefined,
		};
	}
	const detail = record.detail;
	if (typeof detail === "string") return { message: detail };
	if (Array.isArray(detail)) {
		const first = detail.find(
			(item): item is { msg: string } =>
				typeof item === "object" && item !== null && typeof (item as { msg?: unknown }).msg === "string"
		);
		return { message: first?.msg ?? "" };
	}
	return { message: "" };
}

/** The wording a person should see for any thrown value. */
export function errorMessage(error: unknown, fallback: string): string {
	if (error instanceof ApiError || error instanceof NetworkError) {
		return error.message || fallback;
	}
	return fallback;
}
