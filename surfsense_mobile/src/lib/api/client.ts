import type { ZodType } from "zod";
import { tokenResponseSchema } from "./schemas";
import { AbortedError, ApiError, NetworkError, SessionExpiredError, isAbortError, parseErrorBody } from "./errors";

/**
 * The session the app keeps: the same pair the Electron desktop app holds.
 * ``accessExpiresAt`` is epoch seconds, as the backend reports it.
 */
export interface TokenPair {
	accessToken: string;
	refreshToken: string;
	accessExpiresAt: number;
}

export interface TokenStore {
	load(): Promise<TokenPair | null>;
	save(tokens: TokenPair): Promise<void>;
	clear(): Promise<void>;
}

/** The subset of fetch the client relies on, so tests can pass a fake. */
export type FetchLike = (url: string, init: RequestInit) => Promise<Response>;

export type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

export interface RequestOptions<T> {
	method?: Method;
	/** JSON-encoded unless it is FormData or a string. */
	body?: unknown;
	schema?: ZodType<T>;
	signal?: AbortSignal;
	headers?: Record<string, string>;
}

export interface ApiClientOptions {
	baseUrl: string;
	store: TokenStore;
	fetch?: FetchLike;
	/** Called once when the session is gone for good (refresh refused). */
	onSessionExpired?: () => void;
	/** Epoch milliseconds; injectable for tests. */
	now?: () => number;
}

/** Refresh this long before the access token runs out, so requests never race its expiry. */
const EXPIRY_MARGIN_SECONDS = 60;

const CLIENT_HEADERS = {
	// Informational (the web sends "web"/"desktop"); the backend does not branch
	// on it. Chat requests carry their own client_platform field.
	"X-SurfSense-Client-Platform": "mobile",
};

export function normalizeBaseUrl(raw: string): string {
	let url = raw.trim();
	if (!url) return url;
	if (!/^https?:\/\//i.test(url)) url = `https://${url}`;
	return url.replace(/\/+$/, "");
}

/** A server address worth trying: http(s) with a dotted host, localhost or an IP. */
export function isPlausibleServerUrl(raw: string): boolean {
	try {
		const url = new URL(normalizeBaseUrl(raw));
		if (url.protocol !== "http:" && url.protocol !== "https:") return false;
		return url.hostname === "localhost" || url.hostname.includes(".") || url.hostname.includes(":");
	} catch {
		return false;
	}
}

/**
 * Talks to one SurfSense backend with a bearer session.
 *
 * Tokens follow the desktop flow: ``POST /auth/desktop/login`` returns an
 * access/refresh pair; ``POST /auth/jwt/refresh`` with the refresh token in the
 * body rotates both (header mode, no cookies); ``POST /auth/jwt/revoke`` ends
 * the device session. Refresh tokens are single-use, so concurrent 401s share
 * one refresh instead of each spending the token.
 */
export class ApiClient {
	private baseUrl: string;
	private readonly store: TokenStore;
	private readonly fetchImpl: FetchLike;
	private readonly now: () => number;
	private onSessionExpired?: () => void;
	private tokens: TokenPair | null = null;
	private loaded = false;
	private refreshing: Promise<boolean> | null = null;

	constructor(options: ApiClientOptions) {
		this.baseUrl = normalizeBaseUrl(options.baseUrl);
		this.store = options.store;
		// Called unbound: a browser's fetch throws "Illegal invocation" when it
		// runs with ``this`` set to anything but the window.
		const fetchFn = options.fetch ?? ((url: string, init: RequestInit) => fetch(url, init));
		this.fetchImpl = (url, init) => fetchFn(url, init);
		this.now = options.now ?? (() => Date.now());
		this.onSessionExpired = options.onSessionExpired;
	}

	get serverUrl(): string {
		return this.baseUrl;
	}

	setBaseUrl(url: string): void {
		this.baseUrl = normalizeBaseUrl(url);
	}

	setSessionExpiredHandler(handler: (() => void) | undefined): void {
		this.onSessionExpired = handler;
	}

	url(path: string): string {
		if (/^https?:\/\//i.test(path)) return path;
		return `${this.baseUrl}${path.startsWith("/") ? "" : "/"}${path}`;
	}

	private async ensureLoaded(): Promise<void> {
		if (this.loaded) return;
		this.tokens = await this.store.load();
		this.loaded = true;
	}

	async hasSession(): Promise<boolean> {
		await this.ensureLoaded();
		return this.tokens !== null;
	}

	private async setTokens(tokens: TokenPair | null): Promise<void> {
		this.tokens = tokens;
		this.loaded = true;
		if (tokens) await this.store.save(tokens);
		else await this.store.clear();
	}

	private async rawFetch(url: string, init: RequestInit): Promise<Response> {
		try {
			return await this.fetchImpl(url, init);
		} catch (error) {
			if (isAbortError(error)) throw new AbortedError();
			if (typeof __DEV__ !== "undefined" && __DEV__ && process.env.NODE_ENV !== "test") {
				console.warn("request failed", url, error);
			}
			throw new NetworkError(undefined, error);
		}
	}

	private async errorFrom(response: Response): Promise<ApiError> {
		let body: unknown = null;
		try {
			body = await response.json();
		} catch {
			// Not JSON (a proxy error page, say): fall back to the status text.
		}
		const parsed = parseErrorBody(body);
		return new ApiError(
			parsed.message || response.statusText || `Request failed (${response.status})`,
			response.status,
			parsed.code,
			parsed.requestId ?? response.headers.get("X-Request-ID") ?? undefined
		);
	}

	/** Email/password sign-in, exactly as the desktop app does it. */
	async login(email: string, password: string): Promise<void> {
		const response = await this.rawFetch(this.url("/auth/desktop/login"), {
			method: "POST",
			headers: { ...CLIENT_HEADERS, "Content-Type": "application/json" },
			body: JSON.stringify({ email: email.trim(), password }),
			credentials: "omit",
		});
		if (!response.ok) throw await this.errorFrom(response);
		const parsed = tokenResponseSchema.parse(await response.json());
		if (!parsed.refresh_token) throw new ApiError("The server did not return a session.", 500);
		await this.setTokens({
			accessToken: parsed.access_token,
			refreshToken: parsed.refresh_token,
			accessExpiresAt: parsed.access_expires_at,
		});
	}

	/** Revoke this device's refresh token (best effort) and forget the session. */
	async logout(): Promise<void> {
		await this.ensureLoaded();
		const refreshToken = this.tokens?.refreshToken;
		await this.setTokens(null);
		if (!refreshToken) return;
		try {
			await this.rawFetch(this.url("/auth/jwt/revoke"), {
				method: "POST",
				headers: { ...CLIENT_HEADERS, "Content-Type": "application/json" },
				body: JSON.stringify({ refresh_token: refreshToken }),
				credentials: "omit",
			});
		} catch {
			// Offline: the token simply expires on the server.
		}
	}

	/** Rotate the pair. Shared by every caller that asks while one is in flight. */
	refresh(): Promise<boolean> {
		if (!this.refreshing) {
			this.refreshing = this.doRefresh().finally(() => {
				this.refreshing = null;
			});
		}
		return this.refreshing;
	}

	private async doRefresh(): Promise<boolean> {
		await this.ensureLoaded();
		const refreshToken = this.tokens?.refreshToken;
		if (!refreshToken) return false;
		let response: Response;
		try {
			response = await this.rawFetch(this.url("/auth/jwt/refresh"), {
				method: "POST",
				headers: { ...CLIENT_HEADERS, "Content-Type": "application/json" },
				body: JSON.stringify({ refresh_token: refreshToken }),
				credentials: "omit",
			});
		} catch (error) {
			// Offline is not a lost session: keep the tokens and let the caller fail.
			if (error instanceof NetworkError || error instanceof AbortedError) throw error;
			return false;
		}
		if (response.status === 401 || response.status === 403) {
			await this.setTokens(null);
			return false;
		}
		if (!response.ok) return false;
		const parsed = tokenResponseSchema.safeParse(await response.json().catch(() => null));
		if (!parsed.success) return false;
		await this.setTokens({
			accessToken: parsed.data.access_token,
			// Rotation hands back a new refresh token; keep the old one only if it did not.
			refreshToken: parsed.data.refresh_token ?? refreshToken,
			accessExpiresAt: parsed.data.access_expires_at,
		});
		return true;
	}

	/** A usable access token, refreshed ahead of expiry; null when signed out. */
	async accessToken(): Promise<string | null> {
		await this.ensureLoaded();
		if (!this.tokens) return null;
		const nowSeconds = this.now() / 1000;
		if (this.tokens.accessExpiresAt - EXPIRY_MARGIN_SECONDS <= nowSeconds) {
			const ok = await this.refresh();
			if (!ok) return null;
		}
		return this.tokens?.accessToken ?? null;
	}

	/** Headers for native loaders (audio, video, images, downloads) that fetch on their own. */
	async authHeaders(): Promise<Record<string, string>> {
		const token = await this.accessToken();
		if (!token) throw this.expire();
		return { ...CLIENT_HEADERS, Authorization: `Bearer ${token}` };
	}

	private expire(): SessionExpiredError {
		this.onSessionExpired?.();
		return new SessionExpiredError();
	}

	/**
	 * An authenticated request that returns the raw response (for streams and
	 * files). A 401 triggers one shared refresh and one retry; if that fails the
	 * session is over.
	 */
	async fetchAuthed(
		path: string,
		init: RequestInit = {},
		fetchImpl: FetchLike = (url, requestInit) => this.rawFetch(url, requestInit)
	): Promise<Response> {
		const send = async (token: string) => {
			const headers = new Headers(init.headers);
			for (const [key, value] of Object.entries(CLIENT_HEADERS)) headers.set(key, value);
			headers.set("Authorization", `Bearer ${token}`);
			try {
				return await fetchImpl(this.url(path), { ...init, headers, credentials: "omit" });
			} catch (error) {
				if (error instanceof NetworkError || error instanceof AbortedError) throw error;
				if (isAbortError(error)) throw new AbortedError();
				throw new NetworkError();
			}
		};
		const token = await this.accessToken();
		if (!token) throw this.expire();
		const first = await send(token);
		if (first.status !== 401) return first;
		const refreshed = await this.refresh();
		if (!refreshed || !this.tokens) throw this.expire();
		const second = await send(this.tokens.accessToken);
		if (second.status === 401) {
			await this.setTokens(null);
			throw this.expire();
		}
		return second;
	}

	/** JSON request with typed errors; the schema fills defaults the web schema declares. */
	async request<T = unknown>(path: string, options: RequestOptions<T> = {}): Promise<T> {
		const { method = "GET", body, schema, signal, headers = {} } = options;
		const init: RequestInit = { method, signal, headers: { ...headers } };
		if (body !== undefined) {
			if (typeof FormData !== "undefined" && body instanceof FormData) {
				init.body = body;
			} else if (typeof body === "string") {
				init.body = body;
			} else {
				init.body = JSON.stringify(body);
				(init.headers as Record<string, string>)["Content-Type"] = "application/json";
			}
		}
		const response = await this.fetchAuthed(path, init);
		if (!response.ok) throw await this.errorFrom(response);
		if (response.status === 204) return null as T;
		const text = await response.text();
		if (!text) return null as T;
		let data: unknown;
		try {
			data = JSON.parse(text);
		} catch {
			throw new ApiError("The server sent an unreadable response.", response.status);
		}
		if (!schema) return data as T;
		const parsed = schema.safeParse(data);
		if (parsed.success) return parsed.data;
		// Same stance as the web client: a contract drift is logged, not fatal.
		console.warn(`Unexpected response shape from ${path}`, parsed.error.issues.slice(0, 3));
		return data as T;
	}

	get<T>(path: string, schema?: ZodType<T>, signal?: AbortSignal) {
		return this.request<T>(path, { schema, signal });
	}
	post<T>(path: string, body?: unknown, schema?: ZodType<T>) {
		return this.request<T>(path, { method: "POST", body, schema });
	}
	patch<T>(path: string, body?: unknown, schema?: ZodType<T>) {
		return this.request<T>(path, { method: "PATCH", body, schema });
	}
	put<T>(path: string, body?: unknown, schema?: ZodType<T>) {
		return this.request<T>(path, { method: "PUT", body, schema });
	}
	delete<T>(path: string, schema?: ZodType<T>) {
		return this.request<T>(path, { method: "DELETE", schema });
	}
}
