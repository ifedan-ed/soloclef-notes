import { writeFileSync } from "node:fs";
import type { Locator, Page } from "@playwright/test";
import { addNoteSource, completeOutput, listStudioOutputs } from "../helpers/api/notebooks";
import { expect, openNotebook, test } from "./fixtures";

/**
 * Live updates (GET /api/v1/events, features/live-changes): what one tab
 * does shows in another open tab of the same account without a reload.
 *
 * Each test has two tabs on one notebook: the actor, which does the thing,
 * and the watcher, which only looks. The watcher never reloads (a mark set
 * on its window would be lost), so whatever it shows came through the
 * change stream and the reads it made stale. Each status a row showed is
 * recorded as the watcher drew it, so a test sees the in-between states
 * (processing, generating) and not only where they ended.
 *
 * Screenshots go to E2E_SCREENSHOT_DIR when it is set, one per state and
 * project (desktop and phone).
 */

const SHOTS = process.env.E2E_SCREENSHOT_DIR;

async function shot(page: Page, project: string, name: string) {
	if (SHOTS) await page.screenshot({ path: `${SHOTS}/${project}-${name}.png` });
}

/** A mark on the window: a reload loses it. */
async function markLoaded(page: Page) {
	await page.evaluate(() => {
		(window as unknown as { __liveMark: number }).__liveMark = 1;
	});
}

async function expectNotReloaded(page: Page) {
	expect(
		await page.evaluate(() => (window as unknown as { __liveMark?: number }).__liveMark),
		"the watching tab was reloaded"
	).toBe(1);
}

/**
 * Records every data-status the rows matching testId show, from now on.
 * A MutationObserver, so a state the page shows for a moment is seen too.
 */
async function recordStatuses(page: Page, testId: string) {
	await page.evaluate((id) => {
		const seen: string[] = [];
		(window as unknown as Record<string, string[]>)[`__seen_${id}`] = seen;
		const note = () => {
			for (const el of document.querySelectorAll(`[data-testid="${id}"]`)) {
				const status = el.getAttribute("data-status");
				if (status && seen[seen.length - 1] !== status) seen.push(status);
			}
		};
		new MutationObserver(note).observe(document.body, {
			subtree: true,
			childList: true,
			attributes: true,
			attributeFilter: ["data-status"],
		});
		note();
	}, testId);
}

async function statusesSeen(page: Page, testId: string): Promise<string[]> {
	return page.evaluate(
		(id) => (window as unknown as Record<string, string[]>)[`__seen_${id}`] ?? [],
		testId
	);
}

/** The unread count on the Notifications button (0 when it shows none). */
async function unreadBadge(bell: Locator): Promise<number> {
	const text = (await bell.textContent())?.trim() ?? "";
	const count = Number.parseInt(text, 10);
	return Number.isNaN(count) ? 0 : count;
}

test.describe("Live updates, signed in through the form", () => {
	// No stored session: this tab signs in as a person does.
	test.use({ storageState: { cookies: [], origins: [] } });

	test("after signing in, an upload in one tab shows in another, from processing to ready", async ({
		page,
		context,
		notebook,
	}, testInfo) => {
		const project = testInfo.project.name;
		await page.goto("/login");
		await page.getByLabel("Email").fill(process.env.PLAYWRIGHT_TEST_EMAIL ?? "");
		await page
			.getByLabel("Password", { exact: true })
			.fill(process.env.PLAYWRIGHT_TEST_PASSWORD ?? "");
		// The signed-in shell opens the change stream once the session is known.
		const stream = page.waitForResponse((r) => r.url().endsWith("/api/v1/events"), { timeout: 30_000 });
		await page.getByRole("button", { name: "Sign in" }).click();
		await expect(page).toHaveURL(/\/dashboard\/notebooks/, { timeout: 30_000 });
		const opened = await stream;
		expect(opened.status()).toBe(200);
		expect(opened.headers()["content-type"]).toContain("text/event-stream");

		await openNotebook(page, notebook.id, "Sources");
		await markLoaded(page);
		await recordStatuses(page, "notebook-source");

		const actor = await context.newPage();
		await openNotebook(actor, notebook.id, "Sources");
		await actor.getByRole("button", { name: "Add sources" }).first().click();
		const dialog = actor.getByRole("dialog", { name: "Add sources" });
		await dialog.getByRole("tab", { name: /Upload/ }).click();
		// Long enough that indexing it takes a few seconds, so the watcher
		// shows it processing before it is ready (a short note is indexed
		// before the watcher has read the list again).
		const file = testInfo.outputPath("e2e-live-upload.md");
		const paragraph = "Photosynthesis turns light, water and carbon dioxide into sugar and oxygen. ";
		writeFileSync(
			file,
			// The notebook's id makes the file new to the account: the same
			// text uploaded again is the document already there, ready.
			`# Notebook ${notebook.id}\n\n` +
				Array.from({ length: 1500 }, (_, i) => `## Part ${i + 1}\n\n${paragraph.repeat(8)}\n`).join("\n")
		);
		await dialog.locator('input[type="file"]').first().setInputFiles(file);
		await dialog.getByRole("button", { name: /^Upload 1 file/ }).click();

		const row = page.getByTestId("notebook-source").filter({ hasText: "e2e-live-upload" });
		await expect(row).toBeVisible({ timeout: 60_000 });
		if ((await row.getAttribute("data-status")) !== "ready") await shot(page, project, "upload-processing");
		await expect(row).toHaveAttribute("data-status", "ready", { timeout: 120_000 });
		await shot(page, project, "upload-ready");

		const seen = await statusesSeen(page, "notebook-source");
		testInfo.annotations.push({ type: "source statuses", description: seen.join(" → ") });
		expect(seen.at(-1)).toBe("ready");
		expect(seen.some((s) => s === "pending" || s === "processing")).toBe(true);
		await expectNotReloaded(page);
		await actor.close();
	});
});

test.describe("Live updates", () => {
	test("a Studio output started in one tab shows in another as it is made and when it is ready; its failure reaches the inbox badge", async ({
		page,
		context,
		notebook,
		request,
		token,
		isPhone,
	}, testInfo) => {
		test.setTimeout(240_000);
		const project = testInfo.project.name;
		await addNoteSource(request, token, notebook.id, "Live source", "Photosynthesis facts.");
		await openNotebook(page, notebook.id, "Studio");
		await markLoaded(page);
		await recordStatuses(page, "studio-output");

		const actor = await context.newPage();
		const generate = async () => {
			await openNotebook(actor, notebook.id, "Studio");
			await actor.getByRole("button", { name: "Flashcards", exact: true }).first().click();
			const dialog = actor.getByRole("dialog");
			await expect(dialog).toBeVisible();
			await dialog.getByRole("button", { name: "Generate now" }).click();
			await expect(dialog).toBeHidden();
		};

		// The first: the stack's model takes 20 s over it, and the spec
		// finishes it meanwhile through the seed route, as the runner would.
		await generate();
		const rows = page.getByTestId("studio-output");
		await expect(rows).toHaveCount(1, { timeout: 30_000 });
		await expect(rows.first()).toHaveAttribute("data-status", /queued|running/);
		await shot(page, project, "studio-generating");
		const [first] = await listStudioOutputs(request, token, notebook.id);
		await completeOutput(request, notebook.id, first.id);
		await expect(rows.first()).toHaveAttribute("data-status", "ready", { timeout: 30_000 });
		await shot(page, project, "studio-ready");

		// The second is left to the stack's model, which makes nothing: the
		// run fails, and its inbox row reaches the badge of a third tab, on
		// the notebooks list (the bell is in the app's own navigation).
		const inbox = await context.newPage();
		await inbox.goto("/dashboard/notebooks");
		if (isPhone) await inbox.getByRole("button", { name: "Open menu" }).click();
		const bell = inbox.getByRole("button", { name: "Notifications", exact: true });
		await expect(bell).toBeVisible();
		await markLoaded(inbox);
		const before = await unreadBadge(bell);
		await generate();
		await expect(rows).toHaveCount(2, { timeout: 30_000 });
		await expect(page.locator('[data-testid="studio-output"][data-status="failed"]')).toHaveCount(1, {
			timeout: 90_000,
		});
		await shot(page, project, "studio-failed");
		await expect.poll(() => unreadBadge(bell), { timeout: 30_000 }).toBeGreaterThan(before);
		await shot(inbox, project, "inbox-badge");
		await expectNotReloaded(inbox);
		await inbox.close();

		const seen = await statusesSeen(page, "studio-output");
		testInfo.annotations.push({ type: "studio statuses", description: seen.join(" → ") });
		expect(seen).toContain("ready");
		expect(seen).toContain("failed");
		expect(seen.some((s) => s === "queued" || s === "running")).toBe(true);
		await expectNotReloaded(page);
		await actor.close();
	});
});
