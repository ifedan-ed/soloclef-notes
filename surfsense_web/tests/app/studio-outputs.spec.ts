import type { Locator, Page } from "@playwright/test";
import {
	addNoteSource,
	completeOutput,
	listStudioOutputs,
	type SeedKind,
	seedOutput,
} from "../helpers/api/notebooks";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, openNotebook, test } from "./fixtures";

/**
 * Studio output list and every viewer, against seeded outputs.
 *
 * Outputs are seeded by the test-only route POST /__e2e__/seed/studio-output,
 * which saves real artifacts (files in storage, document rows, indexing)
 * through the production save path and links them as Studio does. Only the
 * generation (sandbox + paid models) is skipped.
 */

const row = (page: Page, title: string) =>
	page.getByRole("button", { name: new RegExp(`^${title}`) }).first();

/**
 * The open output (ui/studio/output-view.tsx): in the widened Studio column or
 * the whole window on a desktop, the whole screen on a phone.
 */
function viewer(page: Page, _isPhone?: boolean): Locator {
	return page.locator("[data-output-view]");
}

/** Close the open output: the header's X on a desktop, Back on a phone. */
async function closeOutput(page: Page, isPhone: boolean) {
	await viewer(page)
		.getByRole("button", { name: isPhone ? "Back to Studio" : "Close", exact: true })
		.click();
	await expect(viewer(page)).toHaveCount(0);
}

async function openOutput(page: Page, ws: number, nb: number, title: string) {
	await openNotebook(page, ws, nb, "Studio");
	const button = row(page, title);
	await expect(button).toBeEnabled({ timeout: 30_000 });
	await button.click();
}

type ViewerCase = {
	kind: SeedKind;
	title: string;
	check: (v: Locator, page: Page) => Promise<void>;
	/** Reports open in the reading view (the output view, expanded). */
	report?: boolean;
};

/** The report reading view: the output view showing a report. */
const reportView = (page: Page, _title?: string) => page.locator('[data-output-view="report"]');

const CASES: ViewerCase[] = [
	{
		kind: "video",
		title: "E2E video",
		check: async (v) => {
			await expect(v.locator("video")).toBeVisible();
			await expect(v.locator("video")).toHaveAttribute(
				"src",
				/\/artifacts\/\d+\/files\/\d+\/content/
			);
		},
	},
	{
		kind: "report",
		report: true,
		title: "E2E report",
		check: async (v) => {
			await expect(v.locator("canvas, [data-page-number], .react-pdf__Page").first()).toBeVisible({
				timeout: 30_000,
			});
		},
	},
	{
		kind: "report_docx",
		report: true,
		title: "E2E report docx",
		check: async (v) => {
			await expect(v.locator("canvas, [data-page-number], .react-pdf__Page").first()).toBeVisible({
				timeout: 30_000,
			});
		},
	},
	{
		kind: "report_html",
		report: true,
		title: "E2E report html",
		check: async (v) => {
			const frame = v.frameLocator('iframe[title="report.html"]');
			await expect(frame.getByRole("heading", { name: "E2E interactive report" })).toBeVisible();
		},
	},
	{
		kind: "slides",
		title: "E2E slides",
		check: async (v) => {
			await expect(v.locator("canvas, [data-page-number], .react-pdf__Page").first()).toBeVisible({
				timeout: 30_000,
			});
		},
	},
	{
		kind: "mindmap",
		title: "E2E mindmap",
		check: async (v) => {
			// The whole hierarchy for screen readers; the canvas opens on the first level.
			const tree = v.getByRole("list", { name: "Mind map" });
			await expect(tree).toContainText("Photosynthesis");
			await expect(tree).toContainText("Calvin cycle");
			await expect(v.getByTestId("mind-map-canvas")).toBeVisible();
			await expect(v.getByRole("button", { name: "Expand Photosynthesis" })).toBeVisible();
		},
	},
	{
		kind: "flashcards",
		title: "E2E flashcards",
		check: async (v) => {
			await expect(v.getByText("E2E question 1")).toBeVisible();
			await v.getByRole("button", { name: "Reveal answer" }).click();
			await expect(v.getByText("E2E answer 1")).toBeVisible();
			await v.getByRole("button", { name: /^Got it/ }).click();
			await expect(v.getByText("E2E question 2")).toBeVisible();
			await expect(v.getByRole("progressbar", { name: "Deck progress" })).toBeVisible();
		},
	},
	{
		kind: "quiz",
		title: "E2E quiz",
		check: async (v) => {
			await expect(v.getByRole("heading", { name: "E2E quiz question 1?" })).toBeVisible();
			await v.getByRole("radio", { name: /Option 1B/ }).click();
			await expect(v.getByText("Correct answer")).toBeAttached();
			// Every option explains itself once answered (NotebookLM's rationales).
			await expect(v.getByText("That's right!")).toBeVisible();
			await expect(v.getByText("Rationale 1A: not this one.")).toBeVisible();
			await expect(v.getByText("Rationale 1D: a common mix-up.")).toBeVisible();
			await v.getByRole("button", { name: "Next" }).click();
			await expect(v.getByRole("heading", { name: "E2E quiz question 2?" })).toBeVisible();
			await expect(v.getByRole("progressbar", { name: "Quiz progress" })).toBeVisible();
		},
	},
	{
		kind: "data_table",
		title: "E2E data table",
		check: async (v) => {
			const sheets = v.getByRole("tablist", { name: "Worksheets" });
			await expect(sheets.getByRole("tab", { name: "Findings" })).toBeVisible();
			await expect(v.getByText("E2E trial A")).toBeVisible();
			await sheets.getByRole("tab", { name: "Notes" }).click();
			await expect(v.getByText("E2E second sheet")).toBeVisible();
		},
	},
	{
		kind: "infographic",
		title: "E2E infographic",
		check: async (v, page) => {
			const img = v.getByRole("img", { name: "infographic.png" });
			await expect(img).toBeVisible();
			await expect
				.poll(() => img.evaluate((el) => (el as HTMLImageElement).naturalWidth))
				.toBeGreaterThan(0);
			await page.getByRole("button", { name: "Zoom in" }).first().click();
		},
	},
];

test.describe("Studio output viewers", () => {
	for (const c of CASES) {
		test(`${c.kind} opens in its viewer`, async ({
			page,
			workspace,
			notebook,
			request,
			isPhone,
		}) => {
			await seedOutput(request, workspace.id, notebook.id, c.kind, "ready", c.title);
			await openOutput(page, workspace.id, notebook.id, c.title);
			const v = c.report ? reportView(page, c.title) : viewer(page, isPhone);
			await expect(v.getByText(c.title).first()).toBeVisible({ timeout: 30_000 });
			await c.check(v, page);
			await expectNoOverflow(page, `${c.kind} viewer`);
		});
	}

	test("audio plays in the Studio's own player with a transcript", async ({
		page,
		workspace,
		notebook,
		request,
	}) => {
		await seedOutput(request, workspace.id, notebook.id, "audio", "ready", "E2E audio");
		await openNotebook(page, workspace.id, notebook.id, "Studio");
		await expect(page.getByRole("button", { name: "Play" }).first()).toBeVisible();
		await row(page, "E2E audio").click();
		await expect(page.getByRole("button", { name: "Back to studio" })).toBeVisible();
		await expect(page.getByRole("heading", { name: "E2E audio" })).toBeVisible();
		const audio = page.locator("audio");
		await expect(audio).toBeAttached();
		await expect
			.poll(() => audio.evaluate((el) => (el as HTMLAudioElement).src))
			.toMatch(/^blob:|\/content/);
		await page.getByRole("button", { name: "View transcript" }).click();
		await expect(page.getByText("Welcome to the E2E overview.")).toBeVisible();
		await expectNoOverflow(page, "audio player");
		await page.getByRole("button", { name: "Back to studio" }).click();
		await expect(row(page, "E2E audio")).toBeVisible();
	});
});

test.describe("Mind map viewer", () => {
	const node = (v: Locator, label: string) =>
		v.getByTestId("mind-map-canvas").locator("[data-mind-map-node]").filter({ hasText: label });
	const scaleOf = async (v: Locator) => {
		const transform = await v
			.getByTestId("mind-map-canvas")
			.locator(":scope > div")
			.first()
			.evaluate((el) => (el as HTMLElement).style.transform);
		const match = /translate\(([-\d.]+)px, ([-\d.]+)px\) scale\(([\d.]+)\)/.exec(transform);
		expect(match, transform).not.toBeNull();
		return { x: Number(match?.[1]), y: Number(match?.[2]), k: Number(match?.[3]) };
	};

	test("expand and collapse, expand all, zoom, pan and download", async ({
		page,
		workspace,
		notebook,
		request,
		isPhone,
	}) => {
		await seedOutput(request, workspace.id, notebook.id, "mindmap", "ready", "E2E map viewer");
		await openOutput(page, workspace.id, notebook.id, "E2E map viewer");
		const v = viewer(page, isPhone);
		const canvas = v.getByTestId("mind-map-canvas");
		await expect(canvas).toBeVisible({ timeout: 30_000 });

		// Root and first level open; deeper topics wait behind their chevron.
		await expect(node(v, "Photosynthesis")).toBeVisible();
		await expect(node(v, "Respiration")).toBeVisible();
		await expect(node(v, "Calvin cycle")).toHaveCount(0);
		await expectNoOverflow(page, "mind map viewer");

		// The whole first level fits the width on open.
		const box = await canvas.boundingBox();
		for (const label of ["E2E mind map", "Photosynthesis", "Respiration"]) {
			const pill = await node(v, label).boundingBox();
			expect(
				pill && box && pill.x >= box.x - 1 && pill.x + pill.width <= box.x + box.width + 1
			).toBe(true);
		}

		const expand = v.getByRole("button", { name: "Expand Photosynthesis" });
		await expect(expand).toHaveAttribute("aria-expanded", "false");
		await expand.click();
		await expect(node(v, "Calvin cycle")).toBeVisible();
		await expect(node(v, "Light reactions")).toBeVisible();
		const collapse = v.getByRole("button", { name: "Collapse Photosynthesis" });
		await expect(collapse).toHaveAttribute("aria-expanded", "true");
		await collapse.click();
		await expect(node(v, "Calvin cycle")).toHaveCount(0);

		const controls = v.getByRole("toolbar", { name: "Mind map controls" });
		await controls.getByRole("button", { name: "Expand all" }).click();
		await expect(node(v, "Krebs cycle")).toBeVisible();
		await expect(node(v, "Calvin cycle")).toBeVisible();
		await expectNoOverflow(page, "mind map expanded");
		await controls.getByRole("button", { name: "Collapse all" }).click();
		await expect(node(v, "Photosynthesis")).toHaveCount(0);
		await expect(node(v, "E2E mind map")).toBeVisible();
		await controls.getByRole("button", { name: "Expand all" }).click();

		const before = await scaleOf(v);
		await controls.getByRole("button", { name: "Zoom in" }).click();
		await expect.poll(async () => (await scaleOf(v)).k).toBeGreaterThan(before.k);
		const zoomed = (await scaleOf(v)).k;
		await controls.getByRole("button", { name: "Zoom out" }).click();
		await expect.poll(async () => (await scaleOf(v)).k).toBeLessThan(zoomed);

		// Drag the empty canvas to pan.
		const start = await scaleOf(v);
		if (box) {
			const x = box.x + 12;
			const y = box.y + box.height - 12;
			if (isPhone) {
				const cdp = await page.context().newCDPSession(page);
				const touch = async (
					type: "touchStart" | "touchMove" | "touchEnd",
					px: number,
					py: number
				) =>
					cdp.send("Input.dispatchTouchEvent", {
						type,
						touchPoints: type === "touchEnd" ? [] : [{ x: px, y: py }],
					});
				await touch("touchStart", x, y);
				for (let i = 1; i <= 5; i++) await touch("touchMove", x + i * 12, y - i * 8);
				await touch("touchEnd", x + 60, y - 40);
			} else {
				await page.mouse.move(x, y);
				await page.mouse.down();
				await page.mouse.move(x + 40, y - 30, { steps: 6 });
				await page.mouse.move(x + 60, y - 40, { steps: 4 });
				await page.mouse.up();
			}
		}
		await expect.poll(async () => (await scaleOf(v)).x).not.toBe(start.x);

		await controls.getByRole("button", { name: "Fit to screen" }).click();
		const download = page.waitForEvent("download");
		await controls.getByRole("button", { name: "Download" }).click();
		await v.getByRole("menuitem", { name: "Download as PNG" }).click();
		expect((await download).suggestedFilename()).toBe("E2E map viewer.png");
		await expectNoOverflow(page, "mind map after zoom and pan");
	});

	test("clicking a topic asks the notebook chat about it", async ({
		page,
		workspace,
		notebook,
		request,
		isPhone,
	}) => {
		await seedOutput(request, workspace.id, notebook.id, "mindmap", "ready", "E2E map ask");
		await openOutput(page, workspace.id, notebook.id, "E2E map ask");
		const v = viewer(page, isPhone);
		const ask = v.getByRole("button", { name: "Ask the chat about Respiration" });
		await expect(ask).toBeVisible({ timeout: 30_000 });
		const sent = page.waitForRequest((r) => r.url().includes("/new_chat") && r.method() === "POST");
		await ask.click();
		const question =
			"Discuss what these sources say about Respiration, in the larger context of E2E mind map.";
		expect((await sent).postData() ?? "").toContain(question);
		if (isPhone) {
			// The sheet closes and the Chat tab shows the question.
			await expect(v).toBeHidden();
			await expect(page.getByRole("tab", { name: /^Chat/ })).toHaveAttribute(
				"aria-selected",
				"true"
			);
		}
		await expect(page.locator('[data-role="user"]').filter({ hasText: question })).toBeVisible();
		await expectNoOverflow(page, "chat after asking from the mind map");
	});
});

test.describe("Studio output list", () => {
	test("running and failed states, retry, rename and delete", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await seedOutput(request, workspace.id, notebook.id, "quiz", "running", "E2E running quiz");
		await seedOutput(request, workspace.id, notebook.id, "mindmap", "failed", "E2E failed map");
		await seedOutput(request, workspace.id, notebook.id, "flashcards", "ready", "E2E ready cards");
		await openNotebook(page, workspace.id, notebook.id, "Studio");

		await expect(row(page, "E2E running quiz")).toBeDisabled();
		await expect(row(page, "E2E running quiz")).toContainText(/Rendering · 40%/);
		await expect(row(page, "E2E failed map")).toContainText("Failed");
		await expect(row(page, "E2E ready cards")).toBeEnabled();
		await expectNoOverflow(page, "studio output list");

		const menuFor = async (title: string) => {
			const container = page
				.locator("li, div")
				.filter({ has: row(page, title) })
				.last();
			await container.hover();
			await container.getByRole("button", { name: "More actions" }).click();
		};

		await menuFor("E2E failed map");
		await expect(page.getByRole("menuitem", { name: "Try again" })).toBeVisible();
		await expectNoOverflow(page, "output row menu");
		await page.keyboard.press("Escape");

		// Rename inline.
		await menuFor("E2E ready cards");
		await page.getByRole("menuitem", { name: "Rename" }).click();
		const rename = page.getByRole("textbox", { name: "Rename" });
		await rename.fill("E2E renamed cards");
		await page.getByRole("button", { name: "Save" }).click();
		await expect(row(page, "E2E renamed cards")).toBeVisible();

		// Delete with the inline confirm.
		await menuFor("E2E renamed cards");
		await page.getByRole("menuitem", { name: "Delete" }).click();
		const confirm = page.getByRole("alert").filter({ hasText: "Delete “E2E renamed cards”?" });
		await expect(confirm).toBeVisible();
		await expectNoOverflow(page, "output delete confirm");
		await confirm.getByRole("button", { name: "Delete" }).click();
		await expect
			.poll(async () =>
				(await listStudioOutputs(request, token, workspace.id, notebook.id)).map((o) => o.title)
			)
			.not.toContain("E2E renamed cards");
	});
});

/**
 * The report reading view (ui/report/report-viewer.tsx): contents list with
 * scroll-spy, section suggestions from the fake quick model
 * (tests/e2e/fakes/quick_llm.py: flashcards under section 1, a quiz under 2,
 * a mind map under 3), Add → progress card → the finished output embedded,
 * dismiss and sources. The e2e stack cannot generate, so the queued
 * output is finished by POST /__e2e__/seed/complete-output.
 */
function paragraphs(topic: string): string {
	return Array.from(
		{ length: 6 },
		(_, i) =>
			`Paragraph ${i + 1} on ${topic}. ${"The unit runs on routine, clear handovers and early escalation. ".repeat(5)}`
	).join("\n\n");
}

const READING_REPORT = `# E2E orientation report

A short welcome for new residents.

## 1. Rounds

${paragraphs("rounds")}

## 2. Escalation

${paragraphs("escalation")}

## 3. Summary

${paragraphs("the summary")}

## 4. Glossary

${paragraphs("terms")}
`;

async function openContents(page: Page, dialog: Locator, isPhone: boolean): Promise<Locator> {
	if (!isPhone) return dialog.getByRole("navigation", { name: "Contents" });
	await dialog.getByRole("button", { name: "Show or hide contents" }).click();
	const sheet = page.getByRole("dialog", { name: "Contents", exact: true });
	await expect(sheet).toBeVisible();
	await expectNoOverflow(page, "report contents sheet");
	return sheet.getByRole("navigation", { name: "Contents" });
}

test.describe("Report reading view", () => {
	test("contents follow the reading position and jump to a section", async ({
		page,
		workspace,
		notebook,
		request,
		isPhone,
	}) => {
		const title = "E2E reading report";
		await seedOutput(request, workspace.id, notebook.id, "report", "ready", title, {
			markdown: READING_REPORT,
		});
		await openOutput(page, workspace.id, notebook.id, title);
		const dialog = reportView(page, title);
		await expect(
			dialog.getByRole("heading", { name: "E2E orientation report", level: 1 })
		).toBeVisible({ timeout: 30_000 });
		await expectNoOverflow(page, "report reading view");

		let toc = await openContents(page, dialog, isPhone);
		await expect(toc.getByRole("button")).toHaveCount(4);
		await expect(toc.getByRole("button", { name: /Rounds$/ })).toHaveAttribute(
			"aria-current",
			"location"
		);
		if (isPhone) await page.getByRole("button", { name: "Close panel" }).first().click();

		// Scroll-spy: reading the second section marks it current.
		await dialog
			.locator('[data-report-section="1"]')
			.evaluate((el) => el.scrollIntoView({ block: "start" }));
		toc = await openContents(page, dialog, isPhone);
		await expect(toc.getByRole("button", { name: /Escalation$/ })).toHaveAttribute(
			"aria-current",
			"location"
		);

		// Jump: the section comes into view and becomes current.
		await toc.getByRole("button", { name: /Summary$/ }).click();
		await expect(dialog.getByRole("heading", { name: "3. Summary" })).toBeInViewport();
		if (isPhone) {
			await expect(page.getByRole("dialog", { name: "Contents", exact: true })).toHaveCount(0);
			toc = await openContents(page, dialog, isPhone);
		}
		await expect(toc.getByRole("button", { name: /Summary$/ })).toHaveAttribute(
			"aria-current",
			"location"
		);
		if (isPhone) await page.getByRole("button", { name: "Close panel" }).first().click();

		// Desktop: the round list button collapses and restores the contents.
		if (!isPhone) {
			await dialog.getByRole("button", { name: "Show or hide contents" }).click();
			await expect(dialog.getByRole("navigation", { name: "Contents" })).toHaveCount(0);
			await dialog.getByRole("button", { name: "Show or hide contents" }).click();
			await expect(dialog.getByRole("navigation", { name: "Contents" })).toBeVisible();
		}

		await closeOutput(page, isPhone);
		await expect(dialog).toHaveCount(0);
	});

	test("a suggestion is added, made in place and embedded; dismissals stick", async ({
		page,
		workspace,
		notebook,
		request,
		token,
		isPhone,
	}) => {
		await addNoteSource(
			request,
			token,
			workspace.id,
			notebook.id,
			"E2E handbook",
			"E2E handbook text about rounds, escalation and handovers."
		);
		const title = "E2E suggestion report";
		await seedOutput(request, workspace.id, notebook.id, "report", "ready", title, {
			markdown: READING_REPORT,
		});
		await openOutput(page, workspace.id, notebook.id, title);
		const dialog = reportView(page, title);

		// Each suggestion sits under the section it deepens.
		const add = dialog.getByRole("button", { name: "Add Quiz: E2E quiz: escalation" });
		await expect(add).toBeVisible({ timeout: 30_000 });
		await expect(dialog.locator('[data-report-section="1"]')).toContainText("E2E quiz: escalation");
		await expect(dialog.locator('[data-report-section="0"]')).toContainText(
			"Recommended · Flashcards"
		);
		await expect(dialog.locator('[data-report-section="2"]')).toContainText("E2E map: summary");
		await expect(dialog.locator('[data-report-section="3"] [data-report-suggestion]')).toHaveCount(
			0
		);
		await expectNoOverflow(page, "report suggestions");

		// Add: a progress card in place, and a normal Studio output.
		await add.click();
		await expect(dialog.getByText("Generating Quiz…")).toBeVisible();
		let outputId = 0;
		await expect
			.poll(async () => {
				const list = await listStudioOutputs(request, token, workspace.id, notebook.id);
				const added = list.find((o) => o.title === "E2E quiz: escalation");
				outputId = added?.id ?? 0;
				return added?.status;
			})
			.toBe("queued");
		await expectNoOverflow(page, "report progress card");

		// Finished: the quiz is embedded where the suggestion was, and works.
		await completeOutput(request, workspace.id, notebook.id, outputId);
		const embed = dialog.getByRole("region", { name: "Quiz: E2E quiz: escalation" });
		await expect(embed.getByRole("heading", { name: "E2E quiz question 1?" })).toBeVisible({
			timeout: 30_000,
		});
		await embed.getByRole("radio", { name: /Option 1B/ }).click();
		await expect(embed.getByText("Correct answer")).toBeAttached();
		await expectNoOverflow(page, "report embedded quiz");

		// Dismiss one; see its sources; no content rating is offered.
		await dialog.getByRole("button", { name: "Dismiss suggestion: E2E cards: rounds" }).click();
		await expect(dialog.getByText("E2E cards: rounds")).toHaveCount(0);
		await expect(dialog.getByRole("button", { name: "Good content" })).toHaveCount(0);
		const chip = dialog.getByRole("button", { name: /^View prompt and 1 source/ });
		await chip.click();
		const sources = dialog.getByRole("region", { name: "View prompt and 1 source" });
		await expect(sources.getByText("E2E handbook")).toBeVisible();
		await expectNoOverflow(page, "report sources");
		await chip.click();
		await expect(sources).toHaveCount(0);

		// Reopened: the dismissal and the embed were kept.
		await closeOutput(page, isPhone);
		await row(page, title).click();
		// (The quiz keeps its place: question 1 was answered, so it shows question 2.)
		await expect(
			dialog.getByRole("region", { name: "Quiz: E2E quiz: escalation" }).getByRole("heading", {
				name: /E2E quiz question \d\?/,
			})
		).toBeVisible({ timeout: 30_000 });
		await expect(
			dialog.getByRole("button", { name: "Add Mind Map: E2E map: summary" })
		).toBeVisible();
		await expect(dialog.getByText("E2E cards: rounds")).toHaveCount(0);
	});

	test("an interactive report keeps its page and gets the contents list", async ({
		page,
		workspace,
		notebook,
		request,
		isPhone,
	}) => {
		const title = "E2E interactive contents";
		const sections = ["Overview", "Workflow", "Escalation"]
			.map(
				(name) =>
					`<h2>${name}</h2>${`<p>${name} detail. ${"Handover and rounds text. ".repeat(20)}</p>`.repeat(6)}`
			)
			.join("");
		await seedOutput(request, workspace.id, notebook.id, "report_html", "ready", title, {
			html: `<h1>E2E interactive page</h1>${sections}`,
		});
		await openOutput(page, workspace.id, notebook.id, title);
		const dialog = reportView(page, title);
		const frame = dialog.frameLocator('iframe[title="report.html"]');
		await expect(frame.getByRole("heading", { name: "E2E interactive page" })).toBeVisible();

		let toc = await openContents(page, dialog, isPhone);
		await expect(toc.getByRole("button")).toHaveCount(3);
		await toc.getByRole("button", { name: /Escalation$/ }).click();
		await expect(frame.getByRole("heading", { name: "Escalation" })).toBeInViewport();
		if (isPhone) toc = await openContents(page, dialog, isPhone);
		await expect(toc.getByRole("button", { name: /Escalation$/ })).toHaveAttribute(
			"aria-current",
			"location"
		);
		await expectNoOverflow(page, "interactive report view");
	});
});
