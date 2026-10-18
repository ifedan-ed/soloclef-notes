/**
 * @jest-environment node
 */
import { z } from "zod";
import { ApiClient, normalizeBaseUrl, type TokenPair, type TokenStore } from "../client";
import { ApiError, NetworkError, SessionExpiredError } from "../errors";
import { notebookSchema } from "../schemas";

class MemoryStore implements TokenStore {
	tokens: TokenPair | null;
	saves = 0;
	constructor(initial: TokenPair | null = null) {
		this.tokens = initial;
	}
	async load() {
		return this.tokens;
	}
	async save(tokens: TokenPair) {
		this.saves += 1;
		this.tokens = tokens;
	}
	async clear() {
		this.tokens = null;
	}
}

type Call = { url: string; method: string; headers: Headers; body: unknown };

const NOW = 1_700_000_000_000;
const FUTURE = NOW / 1000 + 3600;

function json(body: unknown, status = 200, headers: Record<string, string> = {}) {
	return new Response(JSON.stringify(body), {
		status,
		headers: { "Content-Type": "application/json", ...headers },
	});
}

/** A scripted backend: handlers by "METHOD path", recording every call. */
function fakeServer(handlers: Record<string, (call: Call, calls: Call[]) => Response | Promise<Response>>) {
	const calls: Call[] = [];
	const fetchImpl = async (url: string, init: RequestInit) => {
		const path = new URL(url).pathname + new URL(url).search;
		const call: Call = {
			url,
			method: init.method ?? "GET",
			headers: new Headers(init.headers),
			body: typeof init.body === "string" ? JSON.parse(init.body) : init.body,
		};
		calls.push(call);
		const handler = handlers[`${call.method} ${path}`];
		if (!handler) return json({ detail: "Not Found" }, 404);
		return handler(call, calls);
	};
	return { calls, fetchImpl };
}

function session(access = "access-1", refresh = "refresh-1", expiresAt = FUTURE): TokenPair {
	return { accessToken: access, refreshToken: refresh, accessExpiresAt: expiresAt };
}

function client(
	store: TokenStore,
	fetchImpl: ReturnType<typeof fakeServer>["fetchImpl"],
	onSessionExpired?: () => void
) {
	return new ApiClient({
		baseUrl: "https://notes.example.com/",
		store,
		fetch: fetchImpl,
		now: () => NOW,
		onSessionExpired,
	});
}

describe("normalizeBaseUrl", () => {
	it("adds https and drops trailing slashes", () => {
		expect(normalizeBaseUrl(" notes.example.com/ ")).toBe("https://notes.example.com");
		expect(normalizeBaseUrl("http://10.0.0.2:8000//")).toBe("http://10.0.0.2:8000");
	});
});

describe("login", () => {
	it("posts to the desktop login route and stores the pair", async () => {
		const store = new MemoryStore();
		const server = fakeServer({
			"POST /auth/desktop/login": () =>
				json({ access_token: "a", refresh_token: "r", token_type: "bearer", access_expires_at: FUTURE }),
		});
		await client(store, server.fetchImpl).login(" me@example.com ", "secret");
		expect(server.calls[0].url).toBe("https://notes.example.com/auth/desktop/login");
		expect(server.calls[0].body).toEqual({ email: "me@example.com", password: "secret" });
		expect(server.calls[0].headers.get("Authorization")).toBeNull();
		expect(store.tokens).toEqual({ accessToken: "a", refreshToken: "r", accessExpiresAt: FUTURE });
	});

	it("surfaces the backend's LOGIN_BAD_CREDENTIALS detail", async () => {
		const server = fakeServer({
			"POST /auth/desktop/login": () => json({ detail: "LOGIN_BAD_CREDENTIALS" }, 400),
		});
		const error = await client(new MemoryStore(), server.fetchImpl)
			.login("me@example.com", "nope")
			.catch((e) => e);
		expect(error).toBeInstanceOf(ApiError);
		expect(error.status).toBe(400);
		expect(error.message).toBe("LOGIN_BAD_CREDENTIALS");
	});

	it("reads the structured error envelope", async () => {
		const server = fakeServer({
			"POST /auth/desktop/login": () =>
				json({ error: { message: "Too many attempts", code: "RATE_LIMITED", request_id: "req_1" } }, 429),
		});
		const error = await client(new MemoryStore(), server.fetchImpl)
			.login("a@b.co", "x")
			.catch((e) => e);
		expect(error).toMatchObject({
			status: 429,
			code: "RATE_LIMITED",
			requestId: "req_1",
			message: "Too many attempts",
		});
	});
});

describe("authenticated requests", () => {
	it("sends the bearer token and applies schema defaults", async () => {
		const server = fakeServer({
			"GET /api/v1/workspaces/1/notebooks/2": () =>
				json({
					id: 2,
					workspace_id: 1,
					title: "Asthma",
					icon: null,
					created_at: "2026-09-01T00:00:00Z",
					updated_at: "2026-09-01T00:00:00Z",
					source_count: 0,
					enabled_source_count: 0,
				}),
		});
		const api = client(new MemoryStore(session()), server.fetchImpl);
		const notebook = await api.get("/api/v1/workspaces/1/notebooks/2", notebookSchema);
		expect(server.calls[0].headers.get("Authorization")).toBe("Bearer access-1");
		expect(notebook.chat_goal).toBe("default");
		expect(notebook.featured).toBe(false);
	});

	it("treats 204 as an empty success", async () => {
		const server = fakeServer({
			"DELETE /api/v1/x": () => new Response(null, { status: 204 }),
		});
		await expect(client(new MemoryStore(session()), server.fetchImpl).delete("/api/v1/x")).resolves.toBeNull();
	});

	it("refreshes once on 401 and retries with the new token", async () => {
		const store = new MemoryStore(session());
		const server = fakeServer({
			"GET /api/v1/me": (call) =>
				call.headers.get("Authorization") === "Bearer access-2"
					? json({ ok: true })
					: json({ detail: "Unauthorized" }, 401),
			"POST /auth/jwt/refresh": (call) => {
				expect(call.body).toEqual({ refresh_token: "refresh-1" });
				return json({ access_token: "access-2", refresh_token: "refresh-2", access_expires_at: FUTURE });
			},
		});
		const result = await client(store, server.fetchImpl).get("/api/v1/me");
		expect(result).toEqual({ ok: true });
		expect(store.tokens?.refreshToken).toBe("refresh-2");
		expect(server.calls.map((c) => `${c.method} ${new URL(c.url).pathname}`)).toEqual([
			"GET /api/v1/me",
			"POST /auth/jwt/refresh",
			"GET /api/v1/me",
		]);
	});

	it("shares one refresh between concurrent 401s (refresh tokens are single-use)", async () => {
		const store = new MemoryStore(session());
		let refreshes = 0;
		const server = fakeServer({
			"GET /api/v1/a": (call) => (call.headers.get("Authorization") === "Bearer access-2" ? json("a") : json({}, 401)),
			"GET /api/v1/b": (call) => (call.headers.get("Authorization") === "Bearer access-2" ? json("b") : json({}, 401)),
			"POST /auth/jwt/refresh": async () => {
				refreshes += 1;
				await new Promise((r) => setTimeout(r, 10));
				return json({ access_token: "access-2", refresh_token: "refresh-2", access_expires_at: FUTURE });
			},
		});
		const api = client(store, server.fetchImpl);
		await expect(Promise.all([api.get("/api/v1/a"), api.get("/api/v1/b")])).resolves.toEqual(["a", "b"]);
		expect(refreshes).toBe(1);
	});

	it("refreshes ahead of expiry instead of waiting for a 401", async () => {
		const store = new MemoryStore(session("old", "refresh-1", NOW / 1000 + 30));
		const server = fakeServer({
			"POST /auth/jwt/refresh": () =>
				json({ access_token: "fresh", refresh_token: "refresh-2", access_expires_at: FUTURE }),
			"GET /api/v1/me": (call) => json({ auth: call.headers.get("Authorization") }),
		});
		await expect(client(store, server.fetchImpl).get("/api/v1/me")).resolves.toEqual({ auth: "Bearer fresh" });
	});

	it("ends the session when the refresh token is refused", async () => {
		const store = new MemoryStore(session());
		const expired = jest.fn();
		const server = fakeServer({
			"GET /api/v1/me": () => json({}, 401),
			"POST /auth/jwt/refresh": () => json({ detail: "Invalid or expired refresh token" }, 401),
		});
		await expect(client(store, server.fetchImpl, expired).get("/api/v1/me")).rejects.toBeInstanceOf(
			SessionExpiredError
		);
		expect(store.tokens).toBeNull();
		expect(expired).toHaveBeenCalledTimes(1);
	});

	it("keeps the session when the network is down", async () => {
		const store = new MemoryStore(session("old", "refresh-1", NOW / 1000 + 10));
		const failing = async () => {
			throw new TypeError("Network request failed");
		};
		const api = new ApiClient({ baseUrl: "https://x.test", store, fetch: failing, now: () => NOW });
		await expect(api.get("/api/v1/me")).rejects.toBeInstanceOf(NetworkError);
		expect(store.tokens?.refreshToken).toBe("refresh-1");
	});

	it("refuses to call the API without a session", async () => {
		const expired = jest.fn();
		const server = fakeServer({});
		await expect(client(new MemoryStore(), server.fetchImpl, expired).get("/api/v1/me")).rejects.toBeInstanceOf(
			SessionExpiredError
		);
		expect(server.calls).toHaveLength(0);
		expect(expired).toHaveBeenCalled();
	});

	it("maps FastAPI validation errors to their first message", async () => {
		const server = fakeServer({
			"POST /api/v1/thing": () =>
				json({ detail: [{ loc: ["body", "title"], msg: "String should have at most 200 characters" }] }, 422),
		});
		const error = await client(new MemoryStore(session()), server.fetchImpl)
			.post("/api/v1/thing", { title: "x" }, z.unknown())
			.catch((e) => e);
		expect(error).toMatchObject({ status: 422, message: "String should have at most 200 characters" });
	});
});

describe("fetch binding", () => {
	it("calls fetch unbound (a browser's fetch rejects any other this)", async () => {
		const tokens = session();
		const strictFetch = function (this: unknown, _url: string) {
			if (this !== undefined && this !== globalThis) throw new TypeError(`Illegal invocation: ${String(this)}`);
			return Promise.resolve(json({ ok: true }));
		};
		const api = new ApiClient({
			baseUrl: "https://x.test",
			store: new MemoryStore(tokens),
			fetch: strictFetch,
			now: () => NOW,
		});
		await expect(api.get("/api/v1/me")).resolves.toEqual({ ok: true });
	});
});

describe("logout", () => {
	it("revokes the refresh token and clears the store", async () => {
		const store = new MemoryStore(session());
		const server = fakeServer({ "POST /auth/jwt/revoke": () => json({ detail: "Successfully logged out" }) });
		await client(store, server.fetchImpl).logout();
		expect(server.calls[0].body).toEqual({ refresh_token: "refresh-1" });
		expect(store.tokens).toBeNull();
	});

	it("still signs out when offline", async () => {
		const store = new MemoryStore(session());
		const api = new ApiClient({
			baseUrl: "https://x.test",
			store,
			fetch: async () => {
				throw new TypeError("offline");
			},
		});
		await api.logout();
		expect(store.tokens).toBeNull();
	});
});
