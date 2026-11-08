/**
 * Loads Cloudflare's Turnstile script once per page, on demand.
 *
 * Explicit rendering: the widget is placed by `TurnstileWidget`, never by the
 * script scanning the DOM, so React stays the owner of the page.
 */

export const TURNSTILE_SCRIPT_SRC =
	"https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";

/** The header the backend reads the solved token from. */
export const TURNSTILE_TOKEN_HEADER = "X-Turnstile-Token";

export interface TurnstileRenderOptions {
	sitekey: string;
	action?: string;
	theme?: "auto" | "light" | "dark";
	size?: "normal" | "flexible" | "compact";
	"refresh-expired"?: "auto" | "manual" | "never";
	callback?: (token: string) => void;
	"expired-callback"?: () => void;
	"error-callback"?: () => void;
	"timeout-callback"?: () => void;
}

export interface TurnstileApi {
	render: (container: HTMLElement, options: TurnstileRenderOptions) => string;
	reset: (widgetId: string) => void;
	remove: (widgetId: string) => void;
}

declare global {
	interface Window {
		turnstile?: TurnstileApi;
	}
}

let loading: Promise<TurnstileApi> | null = null;

export function loadTurnstile(): Promise<TurnstileApi> {
	if (typeof window === "undefined") {
		return Promise.reject(new Error("Turnstile needs a browser"));
	}
	if (window.turnstile) return Promise.resolve(window.turnstile);
	if (loading) return loading;

	loading = new Promise<TurnstileApi>((resolve, reject) => {
		const script = document.createElement("script");
		script.src = TURNSTILE_SCRIPT_SRC;
		script.async = true;
		script.defer = true;
		script.onload = () => {
			if (window.turnstile) resolve(window.turnstile);
			else reject(new Error("Turnstile script loaded without its API"));
		};
		script.onerror = () => {
			// Let a later mount try again rather than caching the failure.
			loading = null;
			script.remove();
			reject(new Error("Turnstile script failed to load"));
		};
		document.head.appendChild(script);
	});
	return loading;
}

/** Headers carrying the solved token, or none when the check is off. */
export function humanCheckHeaders(token: string | null | undefined): Record<string, string> {
	return token ? { [TURNSTILE_TOKEN_HEADER]: token } : {};
}
