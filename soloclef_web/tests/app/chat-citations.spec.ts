import fs from "node:fs";
import path from "node:path";
import type { Locator, Page } from "@playwright/test";
import { addNoteSource, documentChunkIds } from "../helpers/api/notebooks";
import { assistantMessages, send } from "../helpers/ui/chat";
import { expect, openNotebook, test } from "./fixtures";

/**
 * Citations in a chat answer are numbered 1, 2, 3 in order of first
 * appearance (lib/citations/citation-numbering.ts), and Copy puts the answer
 * on the clipboard as formatted html and clean plain text with those numbers
 * and a Sources list (features/markdown-copy).
 *
 * The answers come from the e2e stack's model stand-in
 * (docker/e2e-web-go/model-stub): "E2E_CITE_RICH:<a>,<b>" writes a formatted
 * answer citing passage a twice, passage b and a web page; "E2E_CITE_LABELS"
 * searches and cites with the labels the search numbered, slowly, so the
 * chips can be watched while the answer streams.
 *
 * SHOTS_DIR=<dir> saves a screenshot at each stage.
 */

async function shot(page: Page, name: string, isPhone: boolean) {
	const dir = process.env.SHOTS_DIR;
	if (!dir) return;
	fs.mkdirSync(dir, { recursive: true });
	await page.screenshot({ path: path.join(dir, `${name}-${isPhone ? "phone" : "desktop"}.png`) });
}

/** The numbers an answer's chips show, in reading order. */
const chipNumbers = (answer: Locator) =>
	answer
		.locator('button[aria-label^="View cited chunk"], [data-slot="citation"], [role="note"]')
		.allInnerTexts();

test("citations show 1, 2, 3 and Copy keeps the formatting", async ({
	page,
	notebook,
	request,
	token,
	isPhone,
}) => {
	const first = await addNoteSource(
		request,
		token,
		notebook.id,
		"Citable source A",
		"# A\n\nThe canary is CITE-ME-91."
	);
	const second = await addNoteSource(
		request,
		token,
		notebook.id,
		"Citable source B",
		"# B\n\nA second canary."
	);
	const [a] = await documentChunkIds(request, token, first.target_id);
	const [b] = await documentChunkIds(request, token, second.target_id);

	await openNotebook(page, notebook.id, "Chat");
	await send(page, `E2E_CITE_RICH:${a},${b} what do the sources say?`);
	const answer = assistantMessages(page).last();
	await expect(answer.getByRole("button", { name: `View cited chunk ${a}` }).first()).toBeVisible({
		timeout: 45_000,
	});
	// a, b, the page, a again (in the table), b again (in the list): the
	// same source keeps its number; the passage ids are never shown.
	await expect.poll(() => chipNumbers(answer)).toEqual(["1", "2", "3", "1", "2"]);
	await shot(page, "chat-citations", isPhone);

	// The numbers are the same after a reload: they come from the saved text.
	await page.reload();
	const reloaded = assistantMessages(page).last();
	await expect(reloaded.getByRole("button", { name: `View cited chunk ${b}` }).first()).toBeVisible(
		{
			timeout: 30_000,
		}
	);
	await expect.poll(() => chipNumbers(reloaded)).toEqual(["1", "2", "3", "1", "2"]);

	// A click still opens exactly the cited passage.
	await reloaded
		.getByRole("button", { name: `View cited chunk ${a}` })
		.first()
		.click();
	const panel = isPhone ? page.getByRole("dialog") : page.locator("body");
	await expect(panel.getByText(/CITE-ME-91/).first()).toBeVisible();
	// The panel (or the phone's sheet) covers the answer: start again from
	// the page, which also proves the copy works on a fresh load.
	await page.reload();
	const answerAgain = assistantMessages(page).last();
	await expect(
		answerAgain.getByRole("button", { name: `View cited chunk ${a}` }).first()
	).toBeVisible({
		timeout: 30_000,
	});

	// Copy: html and plain text, numbered, with the sources after.
	await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
	await answerAgain.hover();
	await answerAgain.getByRole("button", { name: "Copy answer" }).click();
	const copied = await page.evaluate(async () => {
		const [item] = await navigator.clipboard.read();
		return {
			html: await (await item.getType("text/html")).text(),
			plain: await (await item.getType("text/plain")).text(),
		};
	});
	expect(copied.plain).toContain("The canary is CITE-ME-91 [1]. A second source agrees [2]");
	expect(copied.plain).toMatch(
		/Sources\n\[1\] Citable source A\n\[2\] Citable source B\n\[3\] https:\/\/example\.org\/guide$/
	);
	expect(copied.plain).not.toMatch(/\*\*|citation:|##/);
	expect(copied.html).toContain("<strong>CITE-ME-91</strong>");
	expect(copied.html).toContain("<h2>Findings</h2>");
	expect(copied.html).toMatch(/<table[^>]*>/);

	// Pasted into an editor, it is the formatted answer.
	await page.evaluate(() => {
		const box = document.createElement("div");
		box.contentEditable = "true";
		box.id = "paste-target";
		box.style.cssText =
			"position:fixed;inset:16px;z-index:99999;overflow:auto;background:#fff;color:#111;padding:16px;border:2px solid #888;font:14px system-ui";
		// The page's CSS reset flattens headings and lists; give the box a
		// word processor's defaults so the screenshot shows what pasted.
		const style = document.createElement("style");
		style.textContent =
			"#paste-target h2{font-size:20px;font-weight:700;margin:8px 0}#paste-target ul{list-style:disc;padding-left:24px}#paste-target p{margin:6px 0}#paste-target a{color:#1a5fb4;text-decoration:underline}#paste-target th,#paste-target td{border-style:solid}#paste-target table{margin:6px 0}";
		document.head.appendChild(style);
		document.body.appendChild(box);
		box.focus();
	});
	await page.keyboard.press("ControlOrMeta+V");
	const pasted = page.locator("#paste-target");
	await expect(pasted.locator("strong", { hasText: "CITE-ME-91" })).toBeVisible();
	await expect(pasted.locator("table td").first()).toContainText("Dose");
	await expect(pasted).toContainText("[3] https://example.org/guide");
	await expect(pasted).toContainText("r² ≤ α");
	await shot(page, "chat-citations-pasted", isPhone);
});

test("streamed citations keep their numbers when the answer is saved", async ({
	page,
	notebook,
	request,
	token,
	isPhone,
}) => {
	await addNoteSource(
		request,
		token,
		notebook.id,
		"Label source one",
		"LABELCANARY first passage."
	);
	await addNoteSource(
		request,
		token,
		notebook.id,
		"Label source two",
		"LABELCANARY second passage."
	);

	await openNotebook(page, notebook.id, "Chat");
	await send(page, "E2E_CITE_LABELS LABELCANARY");
	const answer = assistantMessages(page).last();
	const streamed = answer.locator('[role="note"]');
	await expect(streamed.first()).toBeVisible({ timeout: 45_000 });
	// Watched while it streams: each chip's number, once shown, never changes.
	const seen: string[][] = [];
	while ((await streamed.count()) > 0) {
		const now = await streamed.allInnerTexts();
		seen.push(now);
		if (now.length === 2) await shot(page, "chat-citations-streaming", isPhone);
		await page.waitForTimeout(100);
	}
	for (const shown of seen) expect(["1", "2", "1"].slice(0, shown.length)).toEqual(shown);
	// Saved: the same numbers, now chips that open their passage.
	await expect.poll(() => chipNumbers(answer)).toEqual(["1", "2", "1"]);
	await expect(answer.locator('button[aria-label^="View cited chunk"]')).toHaveCount(3);
	await shot(page, "chat-citations-saved", isPhone);
});
