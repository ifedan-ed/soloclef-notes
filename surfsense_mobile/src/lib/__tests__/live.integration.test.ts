/**
 * @jest-environment node
 *
 * Read-only probes of a live server with no account: the sign-in, refresh
 * and error paths the app relies on. Nothing is created or changed; the one
 * sign-in attempt uses an address that cannot exist. Opt in with
 *
 *   SURFSENSE_LIVE_URL=https://notes.example.com npm run test:integration -- live
 */
import { ApiClient, type TokenPair, type TokenStore } from "../api/client";
import { createEndpoints } from "../api/endpoints";
import { ApiError, SessionExpiredError } from "../api/errors";

const BASE = process.env.SURFSENSE_LIVE_URL;
const suite = BASE ? describe : describe.skip;

class MemoryStore implements TokenStore {
	constructor(public tokens: TokenPair | null = null) {}
	async load() {
		return this.tokens;
	}
	async save(tokens: TokenPair) {
		this.tokens = tokens;
	}
	async clear() {
		this.tokens = null;
	}
}

suite("live server, no account", () => {
	jest.setTimeout(30_000);
	const fetchImpl = (url: string, init: RequestInit) => fetch(url, init);

	it("serves the API at the app's default address", async () => {
		const response = await fetch(`${BASE}/api/v1/workspaces`);
		expect(response.status).toBe(401);
		expect(response.headers.get("content-type")).toContain("application/json");
	});

	it("rejects unknown credentials with the code the app words as 'wrong email or password'", async () => {
		const api = new ApiClient({ baseUrl: BASE ?? "", store: new MemoryStore(), fetch: fetchImpl });
		const error = await api.login("mobile-smoke-probe@invalid.example", "not-a-real-password").catch((e) => e);
		expect(error).toBeInstanceOf(ApiError);
		expect([400, 429]).toContain(error.status);
		if (error.status === 400) expect(error.message).toBe("LOGIN_BAD_CREDENTIALS");
	});

	it("ends a session whose refresh token the server does not know", async () => {
		const store = new MemoryStore({
			accessToken: "not.a.jwt",
			refreshToken: "not-a-refresh-token",
			accessExpiresAt: 0,
		});
		const expired = jest.fn();
		const api = new ApiClient({ baseUrl: BASE ?? "", store, fetch: fetchImpl, onSessionExpired: expired });
		await expect(createEndpoints(api).workspaces()).rejects.toBeInstanceOf(SessionExpiredError);
		expect(store.tokens).toBeNull();
		expect(expired).toHaveBeenCalled();
	});
});
