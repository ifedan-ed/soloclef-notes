import fs from "node:fs";
import path from "node:path";
import { test as base, expect, type Page } from "@playwright/test";
import { acquireTestToken } from "../helpers/api/auth";
import { createNotebook, type NotebookRow } from "../helpers/api/notebooks";
import { createWorkspace, deleteWorkspace, type WorkspaceRow } from "../helpers/api/workspaces";

/**
 * Fixtures for the app suite (desktop + phone projects, isolated stack only).
 *
 * - `token`: bearer token for the seeded e2e user (reuses the setup cookie).
 * - `workspace`: a fresh workspace per test, deleted afterwards, so tests run
 *   in parallel without seeing each other's notebooks.
 * - `notebook`: an empty notebook in that workspace.
 * - `isPhone`: true in the phone project.
 *
 * Every test also fails on a native alert/confirm/prompt (the app uses inline
 * UI only) and on an uncaught page error.
 */

const AUTH_STATE_PATH = path.join(__dirname, "..", "..", "playwright", ".auth", "user.json");

function cachedToken(): string | null {
	try {
		const raw = JSON.parse(fs.readFileSync(AUTH_STATE_PATH, "utf8")) as {
			cookies?: { name: string; value: string }[];
		};
		return raw.cookies?.find((c) => c.name === "surfsense_session")?.value ?? null;
	} catch {
		return null;
	}
}

type Fixtures = {
	token: string;
	workspace: WorkspaceRow;
	notebook: NotebookRow;
	isPhone: boolean;
	failOnPopups: undefined;
};

export const test = base.extend<Fixtures, { workerToken: string }>({
	workerToken: [
		async ({ playwright }, use) => {
			const cached = cachedToken();
			if (cached) return use(cached);
			const ctx = await playwright.request.newContext();
			try {
				await use(await acquireTestToken(ctx));
			} finally {
				await ctx.dispose();
			}
		},
		{ scope: "worker" },
	],
	token: async ({ workerToken }, use) => use(workerToken),
	workspace: async ({ request, token }, use) => {
		const ws = await createWorkspace(
			request,
			token,
			`web-e2e-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`
		);
		try {
			await use(ws);
		} finally {
			await deleteWorkspace(request, token, ws.id).catch(() => undefined);
		}
	},
	notebook: async ({ request, token, workspace }, use) => {
		await use(await createNotebook(request, token, workspace.id, "E2E notebook"));
	},
	isPhone: async ({ viewport }, use) => use((viewport?.width ?? 1280) < 640),
	failOnPopups: [
		async ({ page }, use, testInfo) => {
			const popups: string[] = [];
			const errors: string[] = [];
			page.on("dialog", async (dialog) => {
				popups.push(`${dialog.type()}: ${dialog.message()}`);
				await dialog.dismiss().catch(() => undefined);
			});
			page.on("pageerror", (err) => errors.push(err.message));
			await use(undefined);
			expect(popups, "native browser popups (use inline UI instead)").toEqual([]);
			if (errors.length) {
				await testInfo.attach("page-errors", { body: errors.join("\n\n") });
			}
			const fatal = errors.filter((e) => !IGNORED_PAGE_ERRORS.some((re) => re.test(e)));
			expect(fatal, "uncaught page errors").toEqual([]);
		},
		{ auto: true },
	],
});

/** Known-harmless errors (aborted fetches during navigation, etc.). */
const IGNORED_PAGE_ERRORS: RegExp[] = [
	/ResizeObserver loop/,
	/AbortError/,
	/The user aborted a request/,
	/NEXT_REDIRECT/,
];

export { expect };

export const nbPath = (ws: number, id: number) => `/dashboard/${ws}/notebooks/${id}`;

/**
 * Open a notebook page and wait until its header has painted.
 * On a phone, optionally switch to one of the Sources / Chat / Studio tabs.
 */
export async function openNotebook(
	page: Page,
	ws: number,
	id: number,
	tab?: "Sources" | "Chat" | "Studio"
): Promise<void> {
	await page.goto(nbPath(ws, id));
	await expect(page.getByRole("textbox", { name: "Notebook title" }).first()).toBeVisible();
	if (tab) await showPanel(page, tab);
	if (tab === "Chat") await waitForChatReady(page);
}

/**
 * The chat has loaded: its welcome card (empty chat) or its messages show.
 * Until then a placeholder composer sits in the footer and is swapped for the
 * welcome card's composer when loading ends.
 */
export async function waitForChatReady(page: Page): Promise<void> {
	await expect(
		page
			.getByText(/Let's start your notebook|Ask your sources/)
			.or(page.locator('[data-role="user"]').first())
	).toBeVisible({ timeout: 30_000 });
}

/** On a phone, pick the tab; on desktop all three panels are already shown. */
export async function showPanel(page: Page, tab: "Sources" | "Chat" | "Studio"): Promise<void> {
	const width = page.viewportSize()?.width ?? 1280;
	if (width >= 1024) return;
	const button = page.getByRole("tab", { name: new RegExp(`^${tab}`) });
	await button.click();
	await expect(button).toHaveAttribute("aria-selected", "true");
}
