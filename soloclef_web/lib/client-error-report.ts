/**
 * Send browser crashes to the backend log (POST /api/v1/client-errors), so a
 * "Something went wrong" page or an uncaught error leaves a line an operator
 * can find, with the page path and the build that crashed.
 *
 * Deliberately small: pathname only (no query string or hash, which can carry
 * tokens), capped message/stack sizes, one report per distinct error, at most
 * a handful per page load, and it can never throw into the caller.
 */
import { APP_VERSION, buildBackendUrl } from "@/lib/env-config";

export type ClientErrorKind =
	| "error-boundary"
	| "global-error"
	| "window-error"
	| "unhandled-rejection";

export interface ClientErrorPayload {
	kind: ClientErrorKind;
	name: string;
	message: string;
	stack: string;
	path: string;
	digest: string;
	build: string;
	request_id: string;
	/** The page reloaded itself to recover (lib/chunk-reload): logged, not a crash anyone saw. */
	recovered_by_reload: boolean;
}

export interface ClientErrorOptions {
	recoveredByReload?: boolean;
}

const MAX_REPORTS_PER_PAGE = 5;
const MAX_MESSAGE = 1000;
const MAX_STACK = 6000;

let sent = 0;
const seen = new Set<string>();

/** The commit this bundle was built from, or the package version. */
export const CLIENT_BUILD_ID = (process.env.NEXT_PUBLIC_BUILD_COMMIT || APP_VERSION || "").slice(
	0,
	64
);

function clip(value: unknown, max: number): string {
	return typeof value === "string" ? value.slice(0, max) : "";
}

/** Pure: turn whatever was thrown into the report body. */
export function buildClientErrorPayload(
	kind: ClientErrorKind,
	error: unknown,
	pathname: string,
	options: ClientErrorOptions = {}
): ClientErrorPayload {
	const err = (error && typeof error === "object" ? error : {}) as {
		name?: unknown;
		message?: unknown;
		stack?: unknown;
		digest?: unknown;
		requestId?: unknown;
	};
	const message =
		typeof error === "string" ? error : clip(err.message, MAX_MESSAGE) || String(error ?? "");
	return {
		kind,
		name: clip(err.name, 100),
		message: message.slice(0, MAX_MESSAGE),
		stack: clip(err.stack, MAX_STACK),
		path: (pathname.split(/[?#]/, 1)[0] ?? "").slice(0, 500),
		digest: clip(err.digest, 100),
		build: CLIENT_BUILD_ID,
		request_id: clip(err.requestId, 64),
		recovered_by_reload: options.recoveredByReload === true,
	};
}

/** For tests: forget what was already reported. */
export function resetClientErrorReports(): void {
	sent = 0;
	seen.clear();
}

/** Returns the payload it sent, or null when skipped. Never throws. */
export function reportClientError(
	kind: ClientErrorKind,
	error: unknown,
	options: ClientErrorOptions = {}
): ClientErrorPayload | null {
	try {
		if (typeof window === "undefined" || sent >= MAX_REPORTS_PER_PAGE) return null;
		const payload = buildClientErrorPayload(kind, error, window.location.pathname, options);
		const key = `${payload.name}|${payload.message}|${payload.path}`;
		if (seen.has(key)) return null;
		seen.add(key);
		sent += 1;
		void fetch(buildBackendUrl("/api/v1/client-errors"), {
			method: "POST",
			credentials: "include",
			keepalive: true,
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify(payload),
		}).catch(() => {});
		return payload;
	} catch {
		return null;
	}
}

let installed = false;

/**
 * Takes an error before it is reported, and answers true when it handled it
 * (lib/chunk-reload's recoverByReload: a chunk that failed to load reloads
 * the page and reports itself). Passed in rather than imported, because
 * that module reports through this one.
 */
export type ClientErrorRecovery = (kind: ClientErrorKind, error: unknown) => boolean;

/** Listen for errors no boundary caught. Idempotent. */
export function installGlobalClientErrorHandlers(recover: ClientErrorRecovery): void {
	if (installed || typeof window === "undefined") return;
	installed = true;
	window.addEventListener("error", (event) => {
		// Resource load failures (img/script 404s) have no `error`; skip them.
		if (event.error && !recover("window-error", event.error)) {
			reportClientError("window-error", event.error);
		}
	});
	// Where a part of the app is loaded outside React (an `await import()` in
	// a click handler), a chunk that fails to load ends up here.
	window.addEventListener("unhandledrejection", (event) => {
		if (!recover("unhandled-rejection", event.reason)) {
			reportClientError("unhandled-rejection", event.reason);
		}
	});
}
