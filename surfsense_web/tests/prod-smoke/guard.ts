import {
	type APIRequestContext,
	type BrowserContext,
	test as base,
	expect,
	type Request,
} from "@playwright/test";

/**
 * Read-only guard for the prod-smoke project.
 *
 * Every request the browser makes that is not a GET/HEAD/OPTIONS is aborted
 * before it leaves the browser, unless it is on READ_ONLY_POSTS below: reads
 * that the app happens to implement as POST. Nothing else can create, edit or
 * delete anything, whatever a test clicks.
 *
 * A few GETs are blocked too, because on the server they spend model tokens
 * and write a cache (they are not needed to check layout):
 *   - /studio/suggestions   (welcome-card questions, Studio dialog ideas)
 *   - /sources/<id>/guide   (source guides)
 *
 * WebSockets (Zero sync) are not routed by Playwright; Zero only writes
 * through POST /api/zero/mutate, which is blocked here.
 */

/** Reads implemented as POST. Keep this list short and explicit. */
export const READ_ONLY_POSTS: { pattern: RegExp; why: string }[] = [
	{ pattern: /\/api\/zero\/query$/, why: "Zero query resolution (reads)" },
	{
		pattern: /\/api\/v1\/messages\/comments\/batch$/,
		why: "loads comment counts for visible messages",
	},
	{
		pattern: /\/api\/v1\/workspaces\/\d+\/notebooks\/\d+\/followups$/,
		why: "follow-up questions (read)",
	},
];

const BLOCKED_GETS: RegExp[] = [/\/studio\/suggestions(\?|$)/, /\/sources\/\d+\/guide(\?|$)/];

const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);

export type BlockedRequest = { method: string; url: string };

export async function installReadOnlyGuard(context: BrowserContext): Promise<BlockedRequest[]> {
	const blocked: BlockedRequest[] = [];
	await context.route("**/*", async (route) => {
		const req: Request = route.request();
		const method = req.method().toUpperCase();
		const url = req.url();
		const allowedPost = method === "POST" && READ_ONLY_POSTS.some((p) => p.pattern.test(url));
		const blockedGet = method === "GET" && BLOCKED_GETS.some((p) => p.test(url));
		if ((!SAFE_METHODS.has(method) && !allowedPost) || blockedGet) {
			blocked.push({ method, url });
			await route.abort("blockedbyclient");
			return;
		}
		await route.continue();
	});
	return blocked;
}

const PROD_BASE_URL = process.env.PROD_BASE_URL || "https://notes.example.com";
const SESSION_COOKIE = process.env.SESSION_COOKIE_NAME || "surfsense_session";

/**
 * Extra cookies for a proxy in front of the app (e.g. an SSO outpost),
 * as "name=value; name2=value2".
 */
function extraCookies(): { name: string; value: string }[] {
	const raw = process.env.PROD_EXTRA_COOKIES ?? "";
	return raw
		.split(";")
		.map((part) => part.trim())
		.filter(Boolean)
		.map((part) => {
			const i = part.indexOf("=");
			return { name: part.slice(0, i), value: part.slice(i + 1) };
		});
}

type ProdFixtures = {
	blocked: BlockedRequest[];
	ws: number;
};

export const test = base.extend<ProdFixtures>({
	context: async ({ context }, use) => {
		const token = process.env.PROD_E2E_TOKEN;
		if (!token) throw new Error("PROD_E2E_TOKEN is not set");
		await context.addCookies(
			[{ name: SESSION_COOKIE, value: token }, ...extraCookies()].map((c) => ({
				...c,
				url: PROD_BASE_URL,
				secure: PROD_BASE_URL.startsWith("https"),
				sameSite: "Lax" as const,
			}))
		);
		if (process.env.PROD_EXTRA_HEADERS) {
			await context.setExtraHTTPHeaders(JSON.parse(process.env.PROD_EXTRA_HEADERS));
		}
		// Keep new-user overlays out of the way (local only, never sent).
		let userId: string | null = null;
		try {
			userId = JSON.parse(Buffer.from(token.split(".")[1] ?? "", "base64").toString()).sub ?? null;
		} catch {}
		await context.addInitScript(
			({ uid }) => {
				try {
					if (uid) localStorage.setItem(`surfsense-tour-${uid}`, "true");
				} catch {}
			},
			{ uid: userId }
		);
		await use(context);
	},
	// Auto: the guard is on before the first navigation of every test.
	blocked: [
		async ({ context }, use, testInfo) => {
			const blocked = await installReadOnlyGuard(context);
			await use(blocked);
			if (blocked.length) {
				await testInfo.attach("blocked-writes", {
					body: blocked.map((b) => `${b.method} ${b.url}`).join("\n"),
				});
			}
		},
		{ auto: true },
	],
	ws: async ({ context, blocked: _blocked }, use) => {
		const fromEnv = Number(process.env.PROD_WORKSPACE_ID);
		if (fromEnv) return use(fromEnv);
		const list = await prodGet<{ id: number }[]>(context.request, "/api/v1/workspaces");
		expect(list.length, "the token's user has a workspace").toBeGreaterThan(0);
		await use(list[0].id);
	},
});

export { expect };

/** GET with the prod token, through the context (so proxy cookies apply). */
export async function prodGet<T>(request: APIRequestContext, path: string): Promise<T> {
	const res = await request.get(`${PROD_BASE_URL}${path}`, {
		headers: { Authorization: `Bearer ${process.env.PROD_E2E_TOKEN}` },
	});
	expect(res.ok(), `GET ${path} (${res.status()})`).toBeTruthy();
	return (await res.json()) as T;
}
