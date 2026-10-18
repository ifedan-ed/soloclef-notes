import type { Page } from "@playwright/test";
import { desktopDevice, phoneDevice } from "../helpers/devices";
import { expectHeaderClearsSafeArea, expectNoOverflow } from "../helpers/layout/overflow";
import { expect, prodGet, test } from "./guard";

/**
 * Read-only smoke against production (default https://notes.example.com).
 *
 * Navigates, opens dialogs and closes them (Escape / Cancel), and runs the
 * layout checks. Never submits anything; the guard in ./guard.ts aborts every
 * write the app might attempt anyway. Run with:
 *
 *   PROD_E2E_TOKEN=<access token> pnpm test:e2e:prod-smoke
 *
 * Optional: PROD_BASE_URL, PROD_WORKSPACE_ID, PROD_NOTEBOOK_ID,
 * PROD_EXTRA_COOKIES ("a=b; c=d", e.g. an SSO proxy's session),
 * PROD_EXTRA_HEADERS (JSON).
 */

type Notebook = { id: number; title: string; source_count: number };
type Output = { id: number; kind: string; status: string; title: string };

async function pickNotebook(page: Page, ws: number): Promise<Notebook | null> {
	const list = await prodGet<Notebook[]>(
		page.context().request,
		`/api/v1/workspaces/${ws}/notebooks`
	);
	const fromEnv = Number(process.env.PROD_NOTEBOOK_ID);
	if (fromEnv) return list.find((n) => n.id === fromEnv) ?? null;
	return [...list].sort((a, b) => b.source_count - a.source_count)[0] ?? null;
}

async function closeDialog(page: Page) {
	await page.keyboard.press("Escape");
	await expect(page.getByRole("dialog")).toHaveCount(0);
}

for (const [viewportName, device] of [
	["desktop", desktopDevice],
	["phone", phoneDevice],
] as const) {
	test.describe(`prod smoke (${viewportName})`, () => {
		// defaultBrowserType cannot be set per describe; the rest can.
		const { defaultBrowserType: _browser, ...viewport } = device;
		test.use(viewport);
		const isPhone = viewportName === "phone";

		test("notebooks home", async ({ page, ws }) => {
			await page.goto(`/dashboard/${ws}/notebooks`);
			await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible({
				timeout: 30_000,
			});
			await expectNoOverflow(page, "home");
			await page.getByRole("radio", { name: "Recent" }).click();
			await expectNoOverflow(page, "home (recent)");
			const more = page.getByRole("button", { name: "More actions" }).first();
			if (await more.isVisible()) {
				await more.click();
				await expectNoOverflow(page, "notebook card menu");
				await page.keyboard.press("Escape");
			}
			if (isPhone) {
				await page.getByRole("button", { name: "Open menu" }).click();
				await expectNoOverflow(page, "navigation sheet");
				await closeDialog(page);
			}
		});

		test("notebook: panels, dialogs, menu", async ({ page, ws }) => {
			const nb = await pickNotebook(page, ws);
			test.skip(!nb, "no notebook in this workspace");
			if (!nb) return;
			await page.goto(`/dashboard/${ws}/notebooks/${nb.id}`);
			const title = page.getByRole("textbox", { name: "Notebook title" }).first();
			await expect(title).toBeVisible({ timeout: 30_000 });
			await expectNoOverflow(page, "notebook");

			const tab = async (name: "Sources" | "Chat" | "Studio") => {
				if (!isPhone) return;
				await page.getByRole("tab", { name: new RegExp(`^${name}`) }).click();
				await expectNoOverflow(page, `notebook tab ${name}`);
			};
			if (isPhone) {
				const header = page.locator("header").filter({ has: title });
				await expectHeaderClearsSafeArea(header);
			}

			// Sources: Add sources dialog, all three tabs; research menus.
			await tab("Sources");
			await page.getByRole("button", { name: "Add sources" }).first().click();
			const add = page.getByRole("dialog", { name: "Add sources" });
			await expect(add).toBeVisible();
			await expectNoOverflow(page, "Add sources: workspace");
			await add.getByRole("tab", { name: /Upload/ }).click();
			await expectNoOverflow(page, "Add sources: upload");
			await add.getByRole("tab", { name: /connector/i }).click();
			await expectNoOverflow(page, "Add sources: connector");
			await closeDialog(page);
			await page.getByRole("button", { name: /^Search in:/ }).click();
			await expectNoOverflow(page, "research source menu");
			await page.keyboard.press("Escape");
			await page.getByRole("button", { name: /^Research mode:/ }).click();
			await expectNoOverflow(page, "research mode menu");
			await page.keyboard.press("Escape");

			// Chat: composer and its pickers (typing only; never sent).
			await tab("Chat");
			const box = page.locator('.aui-composer-root [contenteditable="true"]');
			// A notebook with no chat yet would create one on open; the guard
			// blocks that, so there is no composer to test.
			const hasComposer = await box
				.waitFor({ state: "visible", timeout: 15_000 })
				.then(() => true)
				.catch(() => false);
			if (hasComposer) {
				await box.click();
				await box.pressSequentially("@");
				await expectNoOverflow(page, "@ picker");
				await box.press("Backspace");
				await box.pressSequentially("/");
				await expectNoOverflow(page, "/ prompt picker");
				await box.press("Backspace");
				await page.keyboard.press("Escape");
			} else {
				test.info().annotations.push({
					type: "note",
					description: "notebook has no chat yet (creating one is blocked); composer skipped",
				});
			}

			// Studio: every generate dialog, opened and closed.
			await tab("Studio");
			for (const tile of [
				"Audio Overview",
				"Video Overview",
				"Mind Map",
				"Reports",
				"Flashcards",
				"Quiz",
				"Infographic",
				"Slide Deck",
				"Data Table",
			]) {
				await page.getByRole("button", { name: tile, exact: true }).first().click();
				await expect(page.getByRole("dialog")).toBeVisible();
				await expectNoOverflow(page, `Studio dialog: ${tile}`);
				await closeDialog(page);
			}

			// Notebook menu dialogs, cancelled.
			for (const item of ["Configure chat", "Customize notebook", "Delete chat history"]) {
				await page.getByRole("button", { name: "Notebook options" }).click();
				await expectNoOverflow(page, "notebook menu");
				await page.getByRole("menuitem", { name: item }).click();
				await expect(page.getByRole("dialog")).toBeVisible();
				await expectNoOverflow(page, `dialog: ${item}`);
				await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click();
				await expect(page.getByRole("dialog")).toHaveCount(0);
			}
		});

		test("Studio viewers for existing outputs", async ({ page, ws }) => {
			const nb = await pickNotebook(page, ws);
			test.skip(!nb, "no notebook in this workspace");
			if (!nb) return;
			const outputs = await prodGet<Output[]>(
				page.context().request,
				`/api/v1/workspaces/${ws}/notebooks/${nb.id}/studio/outputs`
			);
			const firstOfKind = new Map<string, Output>();
			for (const o of outputs) {
				if (o.status === "ready" && !firstOfKind.has(o.kind)) firstOfKind.set(o.kind, o);
			}
			test.skip(firstOfKind.size === 0, "no finished Studio outputs in this notebook");
			await page.goto(`/dashboard/${ws}/notebooks/${nb.id}`);
			await expect(page.getByRole("textbox", { name: "Notebook title" }).first()).toBeVisible({
				timeout: 30_000,
			});
			for (const output of firstOfKind.values()) {
				if (isPhone) await page.getByRole("tab", { name: /^Studio/ }).click();
				const name = new RegExp(`^${output.title.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`);
				await page.getByRole("button", { name, disabled: false }).first().click();
				await page.waitForTimeout(1500);
				await expectNoOverflow(page, `viewer: ${output.kind}`);
				// Close: the audio player has "Back to studio"; the output view has
				// Close (desktop) or "Back to Studio" (phone) in its header.
				const back = page.getByRole("button", { name: "Back to studio", exact: true });
				if (await back.isVisible()) await back.click();
				else {
					const view = page.locator("[data-output-view]");
					const close = view.getByRole("button", {
						name: isPhone ? "Back to Studio" : "Close",
						exact: true,
					});
					if (await close.isVisible()) await close.click();
					else await page.keyboard.press("Escape");
				}
			}
		});

		test("settings, team, logs, automations", async ({ page, ws }) => {
			const pages: [string, string][] = [
				["user-settings/profile", "User Settings"],
				["user-settings/appearance", "User Settings"],
				["user-settings/api-key", "User Settings"],
				["user-settings/prompts", "User Settings"],
				["user-settings/community-prompts", "User Settings"],
				["user-settings/agent-permissions", "User Settings"],
				["workspace-settings/general", "Workspace Settings"],
				["workspace-settings/connectors", "Workspace Settings"],
				["workspace-settings/automations", "Workspace Settings"],
				["workspace-settings/models", "Workspace Settings"],
				["workspace-settings/team-roles", "Workspace Settings"],
				["workspace-settings/prompts", "Workspace Settings"],
				["workspace-settings/public-links", "Workspace Settings"],
				["team", "Members"],
				["logs", "Task Logs"],
			];
			for (const [path, heading] of pages) {
				await page.goto(`/dashboard/${ws}/${path}`);
				await expect(page.getByRole("heading", { name: heading }).first()).toBeVisible({
					timeout: 30_000,
				});
				await page.waitForTimeout(800);
				await expectNoOverflow(page, path);
			}
			// Dialogs that only read to open.
			await page.goto(`/dashboard/${ws}/team`);
			await page.getByRole("button", { name: "Invite", exact: true }).click();
			await expectNoOverflow(page, "invite dialog");
			await closeDialog(page);
			// API keys are for administrators; anyone else lands on Profile.
			await page.goto(`/dashboard/${ws}/user-settings/api-key`);
			const createKey = page.getByRole("button", { name: "Create API key" }).first();
			if (await createKey.isVisible({ timeout: 5_000 }).catch(() => false)) {
				await createKey.click();
				await expectNoOverflow(page, "create API key dialog");
				await closeDialog(page);
			}
			await page.goto(`/dashboard/${ws}/user-settings/prompts`);
			await page.getByRole("button", { name: "New", exact: true }).click();
			await expectNoOverflow(page, "new prompt dialog");
			await closeDialog(page);
		});
	});
}
