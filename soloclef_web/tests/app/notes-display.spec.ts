import fs from "node:fs";
import path from "node:path";
import type { Page } from "@playwright/test";
import { plainText } from "@/lib/markdown/plain-text";
import { authHeaders } from "../helpers/api/auth";
import { type NoteMaker, noteUrl, provideNotes } from "../helpers/api/notes";
import { test as base, expect, uniqueName } from "./fixtures";

/**
 * A note opens once on the Notes page, fully drawn: never blank after its
 * first text, never drawn twice, never saved by being opened; its Markdown
 * and maths drawn as a chat answer's are. The note is shaped like the one in
 * the owner's screenshot (2026-10-10): an answer kept with Save to note, its
 * maths in \( \) and \[ \], with a table, code, lists, a link and money, and
 * the raw title the old Save to note gave it.
 *
 * NOTES_DISPLAY_SHOTS=<dir> also keeps a screenshot of each moment while it
 * opens (the frame-by-frame record of the fix).
 */

const ANSWER = [
	"The correlation was moderate and positive: Pearson's \\(r = 0.463\\), statistically significant (\\(p < 0.001\\)).",
	"",
	"## How it was measured",
	"",
	"\\[",
	"r = \\frac{\\sum (x_i - \\bar{x})(y_i - \\bar{y})}{(n-1) s_x s_y}",
	"\\]",
	"",
	"| Measure | Value |",
	"| --- | --- |",
	"| \\(r\\) | 0.463 |",
	"| \\(n\\) | 212 |",
	"",
	"- **Strength:** moderate",
	"- Direction: positive",
	"",
	"1. Check the scatter plot",
	"2. Report the interval",
	"",
	"```python",
	"from scipy.stats import pearsonr",
	"r, p = pearsonr(x, y)",
	"```",
	"",
	"See [the scipy docs](https://docs.scipy.org/doc/scipy/reference/generated/scipy.stats.pearsonr.html). The test cost $5 and $10.",
].join("\n");

const RAW_TITLE =
	"The correlation was moderate and positive: Pearson's \\(r = 0.463\\), statistical…";

type Frame = {
	t: number;
	/** How many editors have been mounted so far (a remount is a second one). */
	mounts: number;
	editors: number;
	text: number;
	skeletons: number;
	katex: number;
	tables: number;
	codeBlocks: number;
	links: number;
	rawTex: boolean;
};

/** Every animation frame from the click on: what the note's pane shows. */
async function startFrameLog(page: Page) {
	await page.evaluate(() => {
		const w = window as unknown as {
			__frames: Frame[];
			__frameStart: number;
			__editor: Element | null;
			__mounts: number;
		};
		w.__frames = [];
		w.__frameStart = performance.now();
		w.__editor = null;
		w.__mounts = 0;
		const tick = () => {
			const pane = document.body;
			const editor = pane.querySelector('[data-slate-editor="true"]');
			if (editor && editor !== w.__editor) {
				w.__editor = editor;
				w.__mounts++;
			}
			const text = (editor?.textContent ?? "").replace(/\s+/g, " ").trim();
			w.__frames.push({
				t: Math.round(performance.now() - w.__frameStart),
				mounts: w.__mounts,
				editors: pane.querySelectorAll('[data-slate-editor="true"]').length,
				text: text.length,
				skeletons: pane.querySelectorAll('[data-slot="skeleton"], .animate-pulse').length,
				katex: editor?.querySelectorAll(".katex").length ?? 0,
				tables: editor?.querySelectorAll("table").length ?? 0,
				codeBlocks: editor?.querySelectorAll("pre").length ?? 0,
				links: editor?.querySelectorAll("a[href]").length ?? 0,
				rawTex: /\\\(|\\\)|\\\[|\\\]/.test(text),
			});
			if (performance.now() - w.__frameStart < 4000) requestAnimationFrame(tick);
		};
		requestAnimationFrame(tick);
	});
}

async function frames(page: Page): Promise<Frame[]> {
	return page.evaluate(() => (window as unknown as { __frames: Frame[] }).__frames);
}

/** Screenshots while the note opens, when NOTES_DISPLAY_SHOTS asks for them. */
async function shoot(page: Page, name: string, until: number) {
	const dir = process.env.NOTES_DISPLAY_SHOTS;
	if (!dir) return;
	fs.mkdirSync(dir, { recursive: true });
	const start = Date.now();
	let n = 0;
	while (Date.now() - start < until) {
		const at = String(Date.now() - start).padStart(4, "0");
		await page.screenshot({
			path: path.join(dir, `${name}-${String(n).padStart(2, "0")}-${at}ms.png`),
		});
		n++;
	}
}

const test = base.extend<{ notes: NoteMaker }>({
	notes: async ({ request, token }, use) => provideNotes(request, token, use),
});

/** The note's text as stored, untrimmed: opening must not have touched a byte of it. */
async function storedContent(page: Page, token: string, id: number): Promise<string> {
	const response = await page.request.get(noteUrl(id), { headers: authHeaders(token) });
	expect(response.ok(), await response.text()).toBeTruthy();
	return ((await response.json()) as { content: string }).content;
}

/**
 * Notes that are not shown in a list are opened from the list as a person
 * does, so each is found there by a name no other test's note has: the
 * account is shared by every test.
 */
const rowFor = (page: Page, tag: string) => page.getByRole("button", { name: tag });

/** Requests that would change the note: its text, its pin, its notebook or its sources. */
function watchWrites(page: Page, id: number): string[] {
	const writes: string[] = [];
	const path = new RegExp(`/api/v1/notes/${id}(/|$)`);
	page.on("request", (req) => {
		if (req.method() !== "GET" && path.test(new URL(req.url()).pathname)) writes.push(req.url());
	});
	return writes;
}

test.describe("A note on the Notes page", () => {
	test.describe.configure({ timeout: 120_000 });
	test("opens once, fully drawn, with an answer's Markdown and maths, unsaved", async ({
		page,
		token,
		notes,
	}, testInfo) => {
		// The raw title the old Save to note gave such an answer, TeX and all,
		// made this test's own by a tag at its end.
		const tag = uniqueName("answer");
		const note = await notes.create({ title: `${RAW_TITLE} ${tag}`, content: ANSWER });

		const saves = watchWrites(page, note.id);

		await page.goto(`/dashboard/notes`);
		const row = rowFor(page, tag);
		await expect(row).toBeVisible({ timeout: 30_000 });
		await row.click();
		await startFrameLog(page);
		await shoot(page, `${testInfo.project.name}-open`, 2500);

		const editor = page.locator('[data-slate-editor="true"]');
		await expect(editor).toContainText("The correlation was moderate and positive");
		// Let any late query, Zero push or autosave land.
		await page.waitForTimeout(2500);
		const log = await frames(page);
		await testInfo.attach("frames.json", { body: JSON.stringify(log, null, 1) });
		if (process.env.NOTES_DISPLAY_SHOTS) {
			fs.writeFileSync(
				path.join(process.env.NOTES_DISPLAY_SHOTS, `${testInfo.project.name}-frames.json`),
				JSON.stringify(log, null, 1)
			);
			await page.screenshot({
				path: path.join(process.env.NOTES_DISPLAY_SHOTS, `${testInfo.project.name}-settled.png`),
				fullPage: true,
			});
		}

		// Once its text shows, it never goes blank, and the editor mounts once.
		const first = log.findIndex((f) => f.text > 0);
		expect(first, "the note's text never showed").toBeGreaterThanOrEqual(0);
		const blanks = log.slice(first).filter((f) => f.text === 0);
		expect.soft(blanks, "frames with the note blank after its text showed").toEqual([]);
		const full = Math.max(...log.map((f) => f.text));
		const shrinks = log.slice(first).filter((f) => f.text < full * 0.9);
		expect.soft(shrinks, "frames drawing less than the whole note after it showed").toEqual([]);
		expect.soft(log.at(-1)?.mounts, "editors mounted while the note opened").toBe(1);

		// Drawn as the chat draws an answer.
		await expect.soft(editor.locator(".katex").first()).toBeVisible({ timeout: 5_000 });
		expect.soft(await editor.locator(".katex").count()).toBeGreaterThanOrEqual(5);
		await expect.soft(editor.locator("table")).toHaveCount(1, { timeout: 5_000 });
		await expect
			.soft(editor.locator("h2"))
			.toContainText("How it was measured", { timeout: 5_000 });
		await expect.soft(editor.locator("pre")).toContainText("pearsonr", { timeout: 5_000 });
		await expect.soft(editor.locator("a[href*='docs.scipy.org']")).toBeVisible({ timeout: 5_000 });
		await expect
			.soft(editor.locator("strong", { hasText: "Strength:" }))
			.toBeVisible({ timeout: 5_000 });
		await expect.soft(editor).toContainText("The test cost $5 and $10.", { timeout: 5_000 });
		expect
			.soft(
				log.filter((f) => f.rawTex),
				"frames showing raw TeX delimiters"
			)
			.toEqual([]);

		// Opening a note is not an edit.
		expect.soft(saves, "saves sent by opening the note").toEqual([]);
		expect.soft(await storedContent(page, token, note.id)).toBe(ANSWER);
	});

	// Notes shaped as the three writers make them (tests/fixtures-files/notes,
	// the phone app's fixtures: Plate's own Markdown, answers kept from a chat,
	// an automation's note document, and the rest of CommonMark and GFM).
	const FIXTURES = path.join(__dirname, "..", "fixtures-files", "notes");
	// The index says which writer made each one; here each is written through
	// the one create route, as every note is, and only its Markdown differs.
	const index = JSON.parse(fs.readFileSync(path.join(FIXTURES, "index.json"), "utf8")) as {
		file: string;
		title: string;
	}[];
	for (const fixture of index) {
		test(`"${fixture.title}" opens once and shows all of its words`, async ({
			page,
			token,
			notes,
		}, testInfo) => {
			const markdown = fs.readFileSync(path.join(FIXTURES, fixture.file), "utf8");
			const tag = uniqueName("fixture");
			const note = await notes.create({ title: `${fixture.title} ${tag}`, content: markdown });
			const saves = watchWrites(page, note.id);

			await page.goto(`/dashboard/notes`);
			const row = rowFor(page, tag);
			await expect(row).toBeVisible({ timeout: 30_000 });
			await row.click();
			await startFrameLog(page);
			const slug = `${testInfo.project.name}-${fixture.file.replace(/\.md$/, "")}`;
			await shoot(page, slug, 1200);
			const editor = page.locator('[data-slate-editor="true"]');
			await expect(editor).toBeVisible();
			await page.waitForTimeout(2500);
			const log = await frames(page);
			if (process.env.NOTES_DISPLAY_SHOTS) {
				fs.writeFileSync(
					path.join(process.env.NOTES_DISPLAY_SHOTS, `${slug}-frames.json`),
					JSON.stringify(log, null, 1)
				);
				await page.screenshot({
					path: path.join(process.env.NOTES_DISPLAY_SHOTS, `${slug}-settled.png`),
					fullPage: true,
				});
			}
			const first = log.findIndex((f) => f.text > 0);
			expect(first, "the note's text never showed").toBeGreaterThanOrEqual(0);
			expect
				.soft(
					log.slice(first).filter((f) => f.text === 0),
					"blank frames"
				)
				.toEqual([]);
			expect.soft(log.at(-1)?.mounts, "editors mounted").toBe(1);
			expect.soft(saves, "saves sent by opening the note").toEqual([]);
			expect.soft(await storedContent(page, token, note.id), "the note as stored").toBe(markdown);

			// Every word the note shows as text is drawn (an image's, a link
			// definition's or a footnote label's words are not text, nor is a
			// citation marker or an entity's name; a code
			// block's language shows as its label).
			const shown = ((await editor.textContent()) ?? "").normalize("NFC").toLowerCase();
			const words = plainText(
				markdown
					.replace(/!\[[^\]]*\]\([^)]*\)|!\[[^\]]*\]\[[^\]]*\]/g, "")
					.replace(/^\[[^\]]+\]:.*$/gm, "")
					.replace(/\[\^[^\]]*\]|\]\[[^\]]*\]/g, "]")
					.replace(/\[citation:[^\]]*\]/g, "")
					.replace(/&[a-z]+;|&#\d+;/gi, " ")
					.replace(/<[^>]+>/g, " ")
			)
				.normalize("NFC")
				.toLowerCase()
				.split(/[^\p{L}\p{N}]+/u)
				.filter((w) => w.length >= 3);
			// plainText drops Markdown's marks, code's underscores too ("weight_kg").
			const bare = shown.replace(/[_*`~]/g, "");
			const lost = [...new Set(words.filter((w) => !shown.includes(w) && !bare.includes(w)))];
			expect.soft(lost, "words of the note not drawn").toEqual([]);
			// Its citation markers as the chat's chips, not as text.
			expect.soft(shown, "citation markers shown as text").not.toContain("[citation:");
			if (/\[citation:/.test(markdown)) {
				await expect.soft(editor.locator("[data-citation]").first()).toBeVisible();
			}
		});
	}
});
