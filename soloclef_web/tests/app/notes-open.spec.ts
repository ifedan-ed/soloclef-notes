import fs from "node:fs";
import path from "node:path";
import type { Locator, Page } from "@playwright/test";
import { authHeaders } from "../helpers/api/auth";
import { type NoteMaker, noteUrl, provideNotes, storedText } from "../helpers/api/notes";
import { noteEditor, openNotesTab } from "../helpers/ui/notes";
import { test as base, expect, nbPath, uniqueName } from "./fixtures";

/**
 * A note shaped like a chat answer kept with Save to note, opened on the
 * Notes page: it is drawn once, whole, and never blank; its Markdown and maths look as they did in the chat;
 * and opening it writes nothing back.
 *
 * NOTES_OPEN_FRAMES=<dir> also keeps the page's frames while the note opens.
 */

const test = base.extend<{ notes: NoteMaker }>({
	notes: async ({ request, token }, use) => provideNotes(request, token, use),
});

// The shape of a note the chat's "Save to note" writes: the answer's
// Markdown as it came, TeX between \( \) and \[ \], a table, a list, code,
// a link, a citation and money.
const ANSWER = String.raw`The correlation was moderate and positive: Pearson's \(r = 0.463\), statistically significant (\(p < 0.001\)) [citation:12].

## What it means

- **Effect size:** \(r^2 = 0.214\), so about 21% of the variance is shared.
- The sample was *large* (\(n = 412\)); see [the methods](https://example.com/methods).

| Measure | Value |
|---|---|
| Pearson's r | 0.463 |
| 95% CI | 0.38 to 0.54 |

\[
t = \frac{r\sqrt{n-2}}{\sqrt{1-r^2}}
\]

A grant of $5 per participant and $10 for follow-up.

${"```"}python
from scipy.stats import pearsonr
r, p = pearsonr(x, y)
${"```"}
`;

type Frame = {
	t: number;
	skeleton: boolean;
	editor: boolean;
	text: number;
	katex: number;
	tables: number;
};

/** What the note's panel shows on every animation frame, from the click on. */
async function recordFrames(page: Page): Promise<void> {
	await page.evaluate(() => {
		const w = window as unknown as { __frames: Frame[]; __start: number };
		w.__frames = [];
		w.__start = performance.now();
		const tick = () => {
			const editor = document.querySelector("[data-slate-editor]");
			w.__frames.push({
				t: Math.round(performance.now() - w.__start),
				skeleton: !!document.querySelector('[data-slot="skeleton"]'),
				editor: !!editor,
				text: (editor?.textContent ?? "").replace(/[\s\u200b\ufeff]/g, "").length,
				katex: editor?.querySelectorAll(".katex").length ?? 0,
				tables: editor?.querySelectorAll("table").length ?? 0,
			});
			if (performance.now() - w.__start < 6000) requestAnimationFrame(tick);
		};
		requestAnimationFrame(tick);
	});
}

async function keepScreencast(page: Page, dir: string) {
	fs.mkdirSync(dir, { recursive: true });
	const cdp = await page.context().newCDPSession(page);
	let n = 0;
	cdp.on("Page.screencastFrame", async (frame) => {
		n += 1;
		fs.writeFileSync(
			path.join(dir, `frame-${String(n).padStart(3, "0")}.png`),
			Buffer.from(frame.data, "base64")
		);
		await cdp.send("Page.screencastFrameAck", { sessionId: frame.sessionId }).catch(() => {});
	});
	await cdp.send("Page.startScreencast", { format: "png", everyNthFrame: 1 });
	return async () => {
		await cdp.send("Page.stopScreencast").catch(() => {});
	};
}

/** Requests that would change the note (its autosave, a document's Save). */
function watchWrites(page: Page, path: RegExp): string[] {
	const writes: string[] = [];
	page.on("request", (req) => {
		if (req.method() !== "GET" && path.test(req.url())) writes.push(`${req.method()} ${req.url()}`);
	});
	return writes;
}

async function framesSoFar(page: Page): Promise<Frame[]> {
	return page.evaluate(() => (window as unknown as { __frames: Frame[] }).__frames);
}

/**
 * The caret at the end of `text`'s line, where the next keys go: a click at
 * the end of its last character. A click in the text and then End left the
 * editor's own record of the caret at the click for up to 100 ms (Slate
 * takes the page's selection in at most that often), and a redraw in that
 * time put the caret back there: the keys went in at the click.
 */
async function caretAtEnd(page: Page, editor: Locator, text: string) {
	const line = editor.getByText(text);
	const box = await line.boundingBox();
	if (!box) throw new Error(`"${text}" is not shown`);
	await line.click({ position: { x: box.width - 1, y: box.height / 2 } });
	await page.waitForTimeout(200);
}

/** In-app navigation, as a link in the app does it: the page's caches stay. */
async function goInApp(page: Page, url: string) {
	await page.evaluate((to) => {
		(window as unknown as { next: { router: { push: (url: string) => void } } }).next.router.push(
			to
		);
	}, url);
	await page.waitForURL((current) => `${current.pathname}${current.search}` === url);
}

/** Every PATCH of the note reaches the server `ms` later: a save still on its way. */
async function slowSaves(page: Page, noteId: number, ms: number) {
	await page.route(new RegExp(`/api/v1/notes/${noteId}$`), async (route) => {
		if (route.request().method() === "PATCH") await new Promise((r) => setTimeout(r, ms));
		await route.continue();
	});
}

/**
 * Leaves the open note on the Notes page, so that opening it again mounts it
 * again. A phone shows one screen at a time and has Back; a desktop shows the
 * list beside the note, and opening another note is how it is left there.
 */
async function leaveNote(page: Page, isPhone: boolean, otherTitle: string) {
	if (isPhone) await page.getByRole("button", { name: "Back to notes" }).click();
	else await page.getByRole("button", { name: otherTitle }).click();
}

test.describe("A note opened on the Notes page", () => {
	test("is drawn once, whole, like the chat's answer, and is not rewritten", async ({
		page,
		request,
		token,
		notes,
	}, testInfo) => {
		const title = uniqueName("The correlation was moderate");
		const note = await notes.create({ title, content: ANSWER });
		const writes = watchWrites(page, new RegExp(`/api/v1/notes/${note.id}(/|$)`));

		await page.goto(`/dashboard/notes`);
		const row = page.getByRole("button", { name: title });
		await expect(row).toBeVisible();

		const framesDir = process.env.NOTES_OPEN_FRAMES
			? path.join(process.env.NOTES_OPEN_FRAMES, testInfo.project.name)
			: null;
		const stop = framesDir ? await keepScreencast(page, framesDir) : null;
		await recordFrames(page);
		await row.click();

		const editor = page.locator("[data-slate-editor]").first();
		await expect(editor).toContainText("What it means");
		// Long enough for an autosave (800 ms) to have gone, had there been one.
		await page.waitForTimeout(2500);
		// A click into the text is not an edit either.
		await editor.getByText("What it means").click();
		await page.waitForTimeout(2000);
		await stop?.();
		const frames = await framesSoFar(page);
		await testInfo.attach("frames.json", { body: JSON.stringify(frames, null, 1) });
		if (framesDir) fs.writeFileSync(path.join(framesDir, "frames.json"), JSON.stringify(frames));

		// No placeholder between the list and the note, and once drawn the
		// note is never blank or redrawn smaller.
		expect
			.soft(
				frames.filter((f) => f.skeleton),
				"frames with a placeholder"
			)
			.toEqual([]);
		const firstDrawn = frames.findIndex((f) => f.editor && f.text > 0);
		expect(firstDrawn, "the note was drawn").toBeGreaterThanOrEqual(0);
		const drawn = frames.slice(firstDrawn);
		expect
			.soft(
				drawn.filter((f) => !f.editor || f.text < drawn[0].text),
				"frames after the first draw that show less of the note"
			)
			.toEqual([]);
		// Drawn whole the first time: its five formulas and its table already there.
		expect.soft(drawn[0].katex, "formulas on the first frame").toBe(5);
		expect.soft(drawn[0].tables, "tables on the first frame").toBe(1);

		// The maths as the chat draws it, the money as written, the Markdown as Markdown.
		await expect.soft(editor.locator(".katex-display")).toHaveCount(1);
		await expect.soft(editor).toContainText("$5 per participant and $10 for follow-up");
		await expect.soft(editor).not.toContainText("(r = 0.463)");
		await expect.soft(editor.locator("h2")).toHaveText("What it means");
		await expect
			.soft(editor.locator("table").getByText("Pearson's r", { exact: true }))
			.toBeVisible();
		await expect.soft(editor.locator("table").getByText("0.38 to 0.54")).toBeVisible();
		await expect.soft(editor.locator("a[href='https://example.com/methods']")).toBeVisible();
		await expect.soft(editor.locator("strong", { hasText: "Effect size:" })).toBeVisible();
		await expect.soft(editor.locator("ul li")).toHaveCount(2);
		await expect.soft(editor).toContainText("from scipy.stats import pearsonr");
		// The citation as the chat draws it: a chip, not its marker.
		await expect.soft(editor.locator("[data-citation]")).toHaveCount(1);
		await expect.soft(editor.getByRole("button", { name: "View cited chunk 12" })).toBeVisible();
		await expect.soft(editor).not.toContainText("[citation:");

		// Opened and clicked, it wrote nothing: not its text, its pin or its sources.
		expect.soft(writes, "requests that changed the note while it was only read").toEqual([]);
		const stored = (await (
			await request.get(noteUrl(note.id), { headers: authHeaders(token) })
		).json()) as { content: string };
		expect(stored.content).toBe(ANSWER);
	});

	test("keeps every word typed while a save is on its way", async ({
		page,
		request,
		token,
		notes,
	}) => {
		const title = uniqueName("Typing check");
		const note = await notes.create({ title, content: "First line." });
		// A slow save, so that its answer comes back while typing goes on.
		await slowSaves(page, note.id, 700);
		await page.goto(`/dashboard/notes`);
		await page.getByRole("button", { name: title }).click();
		const editor = noteEditor(page, "First line.");
		await caretAtEnd(page, editor, "First line.");
		await page.keyboard.type(" alpha", { delay: 30 });
		// The autosave goes (800 ms after the last key) and is answered 700 ms later.
		await page.waitForTimeout(900);
		await recordFrames(page);
		await page.keyboard.type(" beta gamma delta", { delay: 60 });
		await page.waitForTimeout(2500);

		// Every word, in the order typed, where the caret was (a phone's End
		// key does not move it), and the same text saved.
		const typed = (await editor.textContent()) ?? "";
		expect(typed).toContain(" alpha beta gamma delta");
		expect(typed.replace(" alpha beta gamma delta", "")).toBe("First line.");
		const lengths = (await framesSoFar(page)).map((f) => f.text);
		expect(
			lengths.filter((n, i) => i > 0 && n < lengths[i - 1]),
			"frames where typed words went missing"
		).toEqual([]);
		await expect.poll(() => storedText(request, token, note.id)).toBe(typed.trim());
	});
});

test.describe("A note in two places, and opened again", () => {
	test("an edit made on the Notes page is what the notebook's Notes tab opens", async ({
		page,
		request,
		token,
		notebook,
		notes,
	}) => {
		const title = uniqueName("Two places");
		const note = await notes.create({ title, content: "Original text.", notebook_id: notebook.id });
		// The notebook's Notes tab lists the note first, so this page keeps its list.
		await openNotesTab(page, notebook.id);
		await expect(page.getByRole("button", { name: title })).toBeVisible();

		await goInApp(page, `/dashboard/notes`);
		await page.getByRole("button", { name: title }).click();
		const editor = noteEditor(page, "Original text.");
		await caretAtEnd(page, editor, "Original text.");
		const saved = page.waitForResponse(
			(r) => r.request().method() === "PATCH" && r.url().endsWith(`/api/v1/notes/${note.id}`)
		);
		await page.keyboard.type(" edited", { delay: 30 });
		await saved;

		// The address a search result links a note by: the notebook, opened at the note.
		await goInApp(page, `${nbPath(notebook.id)}?note=${note.id}`);
		const inNotebook = noteEditor(page, "Original text.");
		await expect(inNotebook).toContainText("Original text. edited");
		await caretAtEnd(page, inNotebook, "Original text. edited");
		await page.keyboard.type(" more", { delay: 30 });
		await expect
			.poll(() => storedText(request, token, note.id), { timeout: 15_000 })
			.toBe("Original text. edited more");
	});

	test("a newer text from elsewhere, there before any typing, is shown and kept", async ({
		page,
		request,
		token,
		notebook,
		notes,
	}) => {
		// Half a minute passes in the test (the page's copy of the notebook's
		// notes is then old enough to be read again), without waiting for it.
		await page.clock.install();
		const title = uniqueName("Elsewhere");
		const note = await notes.create({ title, content: "Original text.", notebook_id: notebook.id });
		await openNotesTab(page, notebook.id);
		await expect(page.getByRole("button", { name: title })).toBeVisible();
		// The phone adds to the note meanwhile.
		const phone = await request.patch(noteUrl(note.id), {
			headers: authHeaders(token),
			data: { content: "Original text. From the phone." },
		});
		expect(phone.ok()).toBeTruthy();
		await page.clock.fastForward("00:31");

		// The note opened in its notebook again: drawn from this page's older
		// copy, then the newer text arrives.
		await goInApp(page, `/dashboard/notes`);
		await goInApp(page, `${nbPath(notebook.id)}?note=${note.id}`);
		const inNotebook = noteEditor(page, "Original text.");
		await expect(inNotebook).toContainText("From the phone.");
		await caretAtEnd(page, inNotebook, "From the phone.");
		await page.keyboard.type(" more", { delay: 30 });
		await expect
			.poll(() => storedText(request, token, note.id), { timeout: 15_000 })
			.toBe("Original text. From the phone. more");
	});

	test("opened again in its notebook before its save lands, the note shows and keeps the last words", async ({
		page,
		request,
		token,
		notebook,
		notes,
	}) => {
		const title = uniqueName("Reopen check");
		const note = await notes.create({ title, content: "First line.", notebook_id: notebook.id });
		await slowSaves(page, note.id, 2500);
		await openNotesTab(page, notebook.id);
		const row = page.getByRole("button", { name: title });
		await row.click();
		const editor = noteEditor(page, "First line.");
		await caretAtEnd(page, editor, "First line.");
		await page.keyboard.type(" alpha", { delay: 30 });
		await page.getByRole("button", { name: "Back to this notebook's notes" }).click();
		await row.click();
		// Its save is still on its way (2.5 s): the words are there all the same.
		await expect(editor).toContainText("First line. alpha", { timeout: 2000 });
		await caretAtEnd(page, editor, "First line. alpha");
		await page.keyboard.type(" beta", { delay: 30 });
		await expect
			.poll(() => storedText(request, token, note.id), { timeout: 15_000 })
			.toBe("First line. alpha beta");
	});

	test("opened again on the Notes page before its save lands, the note shows and keeps the last words", async ({
		page,
		request,
		token,
		notes,
		isPhone,
	}) => {
		const title = uniqueName("Notes reopen");
		const other = uniqueName("Another note");
		const note = await notes.create({ title, content: "First line." });
		await notes.create({ title: other, content: "Something else." });
		await slowSaves(page, note.id, 2500);
		await page.goto(`/dashboard/notes`);
		const row = page.getByRole("button", { name: title });
		await row.click();
		const editor = noteEditor(page, "First line.");
		await caretAtEnd(page, editor, "First line.");
		await page.keyboard.type(" alpha", { delay: 30 });
		await leaveNote(page, isPhone, other);
		await row.click();
		await expect(editor).toContainText("First line. alpha");
		await caretAtEnd(page, editor, "First line. alpha");
		await page.keyboard.type(" beta", { delay: 30 });
		await expect
			.poll(() => storedText(request, token, note.id), { timeout: 15_000 })
			.toBe("First line. alpha beta");
	});

	test("its citations are the chat's chips, and are kept as markers when it is edited", async ({
		page,
		request,
		token,
		notes,
	}) => {
		const title = uniqueName("Cited note");
		const note = await notes.create({
			title,
			content:
				"Start oxygen below 90% [citation:1204, doc-88]; see [citation:https://example.org/hf-trial].\n\nLast line.",
		});
		await page.goto(`/dashboard/notes`);
		await page.getByRole("button", { name: title }).click();
		const editor = noteEditor(page, "Last line.");
		await expect(editor).toContainText("Last line.");
		await expect(editor.locator("[data-citation]")).toHaveCount(3);
		await expect(editor.getByRole("button", { name: "View cited chunk 1204" })).toBeVisible();
		await expect(editor).not.toContainText("[citation:");
		await caretAtEnd(page, editor, "Last line.");
		await page.keyboard.type(" More.", { delay: 30 });
		await expect
			.poll(() => storedText(request, token, note.id), { timeout: 15_000 })
			.toBe(
				"Start oxygen below 90% [citation:1204][citation:doc-88]; see [citation:https://example.org/hf-trial].\n\nLast line. More."
			);
	});
});
