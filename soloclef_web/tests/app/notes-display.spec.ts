import fs from "node:fs";
import path from "node:path";
import type { APIRequestContext, Page } from "@playwright/test";
import { plainText } from "@/lib/markdown/plain-text";
import { authHeaders, BACKEND_URL } from "../helpers/api/auth";
import { createNote } from "../helpers/api/notebooks";
import { expect, test } from "./fixtures";

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

async function renameNote(
	request: APIRequestContext,
	token: string,
	ws: number,
	nb: number,
	id: number,
	title: string
) {
	const res = await request.patch(
		`${BACKEND_URL}/api/v1/workspaces/${ws}/notebooks/${nb}/notes/${id}`,
		{ headers: authHeaders(token), data: { title } }
	);
	expect(res.ok(), await res.text()).toBeTruthy();
}

test.describe("A note on the Notes page", () => {
	test.describe.configure({ timeout: 120_000 });
	test("opens once, fully drawn, with an answer's Markdown and maths, unsaved", async ({
		page,
		request,
		token,
		workspace,
		notebook,
	}, testInfo) => {
		const note = await createNote(request, token, workspace.id, notebook.id, "x", ANSWER);
		// The title the old Save to note gave such an answer (a PATCH keeps it as sent).
		await renameNote(request, token, workspace.id, notebook.id, note.id, RAW_TITLE);

		const saves: string[] = [];
		page.on("request", (req) => {
			if (req.method() !== "GET" && /\/notes\/\d+$|\/save$/.test(req.url())) saves.push(req.url());
		});

		await page.goto(`/dashboard/${workspace.id}/notes`);
		const row = page.getByRole("button", { name: /The correlation was moderate/ });
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
		const stored = await request.get(
			`${BACKEND_URL}/api/v1/workspaces/${workspace.id}/notebooks/${notebook.id}/notes/${note.id}`,
			{ headers: authHeaders(token) }
		);
		expect.soft(((await stored.json()) as { content: string }).content).toBe(ANSWER);
	});

	test("a note document opens once, fully drawn, unsaved", async ({
		page,
		request,
		token,
		workspace,
	}, testInfo) => {
		const created = await request.post(`${BACKEND_URL}/api/v1/workspaces/${workspace.id}/notes`, {
			headers: authHeaders(token),
			data: {
				title: RAW_TITLE.replace("The correlation", "A document: the correlation"),
				source_markdown: ANSWER,
			},
		});
		expect(created.ok(), await created.text()).toBeTruthy();

		await page.goto(`/dashboard/${workspace.id}/notes`);
		const row = page.getByRole("button", { name: /A document: the correlation/ });
		await expect(row).toBeVisible({ timeout: 30_000 });
		await row.click();
		await startFrameLog(page);
		await shoot(page, `${testInfo.project.name}-document`, 2000);
		const editor = page.locator('[data-slate-editor="true"]');
		await expect(editor).toContainText("The correlation was moderate and positive");
		await page.waitForTimeout(2000);
		const log = await frames(page);
		if (process.env.NOTES_DISPLAY_SHOTS) {
			fs.writeFileSync(
				path.join(process.env.NOTES_DISPLAY_SHOTS, `${testInfo.project.name}-document-frames.json`),
				JSON.stringify(log, null, 1)
			);
			await page.screenshot({
				path: path.join(
					process.env.NOTES_DISPLAY_SHOTS,
					`${testInfo.project.name}-document-settled.png`
				),
				fullPage: true,
			});
		}
		const first = log.findIndex((f) => f.text > 0);
		expect.soft(log.slice(first).filter((f) => f.text === 0)).toEqual([]);
		await expect.soft(editor.locator(".katex").first()).toBeVisible({ timeout: 5_000 });
		await expect.soft(editor.locator("table")).toHaveCount(1, { timeout: 5_000 });
		// Unsaved: Save stays off until the note is edited.
		await expect
			.soft(page.getByRole("button", { name: "Save", exact: true }))
			.toBeDisabled({ timeout: 5_000 });
	});

	// Notes shaped as the three writers make them (tests/fixtures-files/notes,
	// the phone app's fixtures: Plate's own Markdown, answers kept from a chat,
	// an automation's note document, and the rest of CommonMark and GFM).
	const FIXTURES = path.join(__dirname, "..", "fixtures-files", "notes");
	const index = JSON.parse(fs.readFileSync(path.join(FIXTURES, "index.json"), "utf8")) as {
		file: string;
		kind: "notebook_note" | "document";
		origin: "hand" | "chat" | "automation";
		title: string;
	}[];
	for (const fixture of index) {
		test(`"${fixture.title}" opens once and shows all of its words`, async ({
			page,
			request,
			token,
			workspace,
			notebook,
		}, testInfo) => {
			const markdown = fs.readFileSync(path.join(FIXTURES, fixture.file), "utf8");
			let stored: () => Promise<string>;
			if (fixture.kind === "notebook_note") {
				const res = await request.post(
					`${BACKEND_URL}/api/v1/workspaces/${workspace.id}/notebooks/${notebook.id}/notes`,
					{
						headers: authHeaders(token),
						data: {
							title: fixture.title,
							content: markdown,
							origin: fixture.origin === "chat" ? "chat" : "hand",
						},
					}
				);
				expect(res.ok(), await res.text()).toBeTruthy();
				const id = ((await res.json()) as { id: number }).id;
				stored = async () =>
					(
						(await (
							await request.get(
								`${BACKEND_URL}/api/v1/workspaces/${workspace.id}/notebooks/${notebook.id}/notes/${id}`,
								{ headers: authHeaders(token) }
							)
						).json()) as { content: string }
					).content;
			} else {
				const res = await request.post(`${BACKEND_URL}/api/v1/workspaces/${workspace.id}/notes`, {
					headers: authHeaders(token),
					data: { title: fixture.title, source_markdown: markdown },
				});
				expect(res.ok(), await res.text()).toBeTruthy();
				const id = ((await res.json()) as { id: number }).id;
				stored = async () =>
					(
						(await (
							await request.get(
								`${BACKEND_URL}/api/v1/workspaces/${workspace.id}/documents/${id}/editor-content`,
								{ headers: authHeaders(token) }
							)
						).json()) as { source_markdown: string }
					).source_markdown;
			}
			const saves: string[] = [];
			page.on("request", (req) => {
				if (req.method() !== "GET" && /\/notes\/\d+$|\/save$/.test(req.url()))
					saves.push(req.url());
			});

			await page.goto(`/dashboard/${workspace.id}/notes`);
			const row = page.getByRole("button", {
				name: new RegExp(fixture.title.slice(0, 12).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")),
			});
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
			expect.soft(await stored(), "the note as stored").toBe(markdown);

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
