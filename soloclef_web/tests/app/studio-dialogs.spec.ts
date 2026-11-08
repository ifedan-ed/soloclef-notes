import type { Page } from "@playwright/test";
import { addNoteSource, listStudioOutputs } from "../helpers/api/notebooks";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, openNotebook, test } from "./fixtures";

/**
 * Studio generate dialogs, one per output type: title, option groups with
 * their defaults ("Default: X" under segmented groups, "(Default)" on cards),
 * the style gallery, focus ideas, and the request "Generate now" sends.
 *
 * Generation itself needs the sandbox and paid models, so it is not run: the
 * spec asserts the POST /studio/generate body. The backend answers audio with
 * a queued run (it then fails offline) and the rest with 503 (sandbox off);
 * both outcomes must show inline, never as a browser popup.
 */

type Segmented = { label: string; def: string; pick?: string };
type Kind = {
	tile: string;
	kind: string;
	title: string;
	cards?: { label: string; def: string };
	segmented?: Segmented[];
	body: Record<string, unknown>;
};

const KINDS: Kind[] = [
	{
		tile: "Audio Overview",
		kind: "audio",
		title: "Customize Audio Overview",
		cards: { label: "Format", def: "Deep Dive" },
		segmented: [{ label: "Length", def: "Default", pick: "Long" }],
		body: { format: "deep_dive", length: "long" },
	},
	{
		tile: "Video Overview",
		kind: "video",
		title: "Customize Video Overview",
		cards: { label: "Format", def: "Explainer" },
		body: { format: "explainer" },
	},
	{
		tile: "Reports",
		kind: "report",
		title: "Create report",
		cards: { label: "Format", def: "Document" },
		segmented: [{ label: "File type", def: "PDF", pick: "Word" }],
		body: { format: "document", file_type: "docx" },
	},
	{
		tile: "Slide Deck",
		kind: "slides",
		title: "Customize slide deck",
		cards: { label: "Format", def: "Detailed deck" },
		segmented: [{ label: "Length", def: "Default", pick: "Short" }],
		body: { length: "short" },
	},
	{
		tile: "Mind Map",
		kind: "mindmap",
		title: "Customize mind map",
		segmented: [{ label: "Level of detail", def: "Standard", pick: "Detailed" }],
		body: { detail: "detailed" },
	},
	{
		tile: "Flashcards",
		kind: "flashcards",
		title: "Customize flashcards",
		segmented: [
			{ label: "Number", def: "Standard", pick: "More" },
			{ label: "Difficulty", def: "Medium", pick: "Hard" },
		],
		body: { count: "more", difficulty: "hard" },
	},
	{
		tile: "Quiz",
		kind: "quiz",
		title: "Customize quiz",
		segmented: [
			{ label: "Number", def: "Standard", pick: "Fewer" },
			{ label: "Difficulty", def: "Medium", pick: "Easy" },
		],
		body: { count: "fewer", difficulty: "easy" },
	},
	{
		tile: "Infographic",
		kind: "infographic",
		title: "Create infographic",
		cards: { label: "Visual style", def: "Auto-select" },
		segmented: [
			{ label: "Orientation", def: "Landscape", pick: "Portrait" },
			{ label: "Level of detail", def: "Standard", pick: "Concise" },
		],
		body: { style: "auto", orientation: "portrait", detail: "concise" },
	},
	{ tile: "Data Table", kind: "data_table", title: "Create data table", body: {} },
];

const studioTile = (page: Page, name: string) =>
	page.getByRole("button", { name, exact: true }).first();

test.describe("Studio dialogs", () => {
	test("without sources a dialog says so and offers to add some", async ({
		page,
		workspace,
		notebook,
	}) => {
		await openNotebook(page, workspace.id, notebook.id, "Studio");
		await studioTile(page, "Quiz").click();
		const dialog = page.getByRole("dialog");
		await expect(dialog).toContainText("has none yet");
		await expect(dialog.getByRole("button", { name: "Add sources" })).toBeVisible();
		await expect(dialog.getByRole("button", { name: "Research the web" })).toBeVisible();
		await expectNoOverflow(page, "studio dialog (no sources)");
	});

	for (const k of KINDS) {
		test(`${k.tile}: options, defaults, ideas, generate request`, async ({
			page,
			workspace,
			notebook,
			request,
			token,
		}) => {
			await addNoteSource(
				request,
				token,
				workspace.id,
				notebook.id,
				"Studio source",
				"Photosynthesis facts."
			);
			await openNotebook(page, workspace.id, notebook.id, "Studio");
			await studioTile(page, k.tile).click();
			const dialog = page.getByRole("dialog", { name: k.title });
			await expect(dialog).toBeVisible();
			await expectNoOverflow(page, `${k.tile} dialog`);

			if (k.cards) {
				const group = dialog.getByRole("group", { name: k.cards.label });
				const def = group.getByRole("button", {
					name: new RegExp(`^${k.cards.def} \\(Default\\)`),
				});
				await expect(def).toHaveAttribute("aria-pressed", "true");
			}
			for (const seg of k.segmented ?? []) {
				await expect(dialog.getByText(`Default: ${seg.def}`, { exact: true })).toBeVisible();
				const group = dialog.getByRole("radiogroup", { name: seg.label });
				await expect(group.getByRole("radio", { name: `${seg.def} (Default)` })).toHaveAttribute(
					"aria-checked",
					"true"
				);
				if (seg.pick) {
					await group.getByRole("radio", { name: seg.pick, exact: true }).click();
					await expect(group.getByRole("radio", { name: seg.pick, exact: true })).toHaveAttribute(
						"aria-checked",
						"true"
					);
				}
			}

			// Focus ideas come from the (fake) quick-tasks model.
			if (k.kind !== "video") {
				const idea = dialog.getByRole("button", { name: /E2E idea/ }).first();
				await expect(idea).toBeVisible({ timeout: 30_000 });
				await idea.click();
				await expect(dialog.locator("#studio-focus")).toHaveValue(/E2E idea/);
			}
			await expect(dialog.getByRole("combobox", { name: "Language" })).toBeVisible();
			await expectNoOverflow(page, `${k.tile} dialog with choices`);

			const generate = page.waitForRequest(
				(r) => r.url().endsWith("/studio/generate") && r.method() === "POST"
			);
			await dialog.getByRole("button", { name: "Generate now" }).click();
			const body = (await generate).postDataJSON() as Record<string, unknown>;
			expect(body).toMatchObject({ kind: k.kind, ...k.body });
			expect(String(body.title)).toContain(k.tile === "Reports" ? "" : "E2E notebook");
			if (k.kind !== "video") expect(String(body.focus ?? "")).toContain("E2E idea");

			if (k.kind === "audio") {
				// Queued: the dialog closes, a toast confirms, the row appears.
				await expect(dialog).toBeHidden();
				await expect(page.getByText("Generating — it will appear in Studio.")).toBeVisible();
				await expect
					.poll(
						async () => (await listStudioOutputs(request, token, workspace.id, notebook.id)).length
					)
					.toBe(1);
				await expect(
					page.getByRole("button", { name: /Audio Overview: E2E notebook/ })
				).toBeVisible();
			} else {
				// Sandbox off in the isolated stack: the 503 shows as an inline
				// toast and the dialog stays open with the choices intact.
				// Exactly one toast: the dialog's own, not a second global one.
				const toastText = page.getByText("Generation is not enabled on this server.");
				await expect(toastText.first()).toBeVisible();
				await page.waitForTimeout(500);
				await expect(toastText).toHaveCount(1);
				await expect(dialog).toBeVisible();
			}
		});
	}

	test("video style gallery: Custom needs a description before Generate", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await addNoteSource(request, token, workspace.id, notebook.id, "Video source", "Cells.");
		await openNotebook(page, workspace.id, notebook.id, "Studio");
		await studioTile(page, "Video Overview").click();
		const dialog = page.getByRole("dialog", { name: "Customize Video Overview" });
		const gallery = dialog.getByRole("group", { name: "Choose visual style" });
		await expect(gallery.getByRole("button", { name: /Auto-select \(Default\)/ })).toHaveAttribute(
			"aria-pressed",
			"true"
		);
		await expectNoOverflow(page, "video style gallery");
		await gallery.getByRole("button", { name: /^Whiteboard/ }).click();
		await expect(gallery.getByRole("button", { name: /^Whiteboard/ })).toHaveAttribute(
			"aria-pressed",
			"true"
		);
		await gallery.getByRole("button", { name: /^Custom/ }).click();
		const describe = dialog.getByRole("textbox", { name: "Describe your style" });
		await expect(describe).toBeVisible();
		await expect(dialog.getByRole("button", { name: "Generate now" })).toBeDisabled();
		await describe.fill("Hand-drawn chalkboard with teal accents");
		await expect(dialog.getByRole("button", { name: "Generate now" })).toBeEnabled();
		await expectNoOverflow(page, "video custom style");
	});

	test("infographic: rocket style gallery, orientation, detail and the exact request", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await addNoteSource(request, token, workspace.id, notebook.id, "Info source", "Iron.");
		await openNotebook(page, workspace.id, notebook.id, "Studio");
		await studioTile(page, "Infographic").click();
		const dialog = page.getByRole("dialog", { name: "Create infographic" });
		await expect(dialog.getByText("Describe the infographic you want to create")).toBeVisible();

		const gallery = dialog.getByRole("group", { name: "Visual style" });
		const styles = [
			"Auto-select",
			"Kawaii",
			"Clay",
			"Sketch Note",
			"Anime",
			"Editorial",
			"Instructional",
			"Bento Grid",
			"Bricks",
			"Scientific",
			"Professional",
		];
		await expect(gallery.getByRole("button")).toHaveCount(styles.length);
		for (const [index, name] of styles.entries()) {
			await expect(gallery.getByRole("button").nth(index)).toHaveAccessibleName(
				new RegExp(`^${name}`)
			);
		}
		// Every card shows its rocket preview.
		const previews = gallery.locator("img");
		await expect(previews).toHaveCount(styles.length);
		for (let i = 0; i < styles.length; i++) {
			await expect
				.poll(() => previews.nth(i).evaluate((el) => (el as HTMLImageElement).naturalWidth))
				.toBeGreaterThan(0);
		}
		await expectNoOverflow(page, "infographic style gallery");

		const bento = gallery.getByRole("button", { name: /^Bento Grid/ });
		await bento.scrollIntoViewIfNeeded();
		await bento.click();
		await expect(bento).toHaveAttribute("aria-pressed", "true");
		await dialog
			.getByRole("radiogroup", { name: "Orientation" })
			.getByRole("radio", { name: "Square", exact: true })
			.click();
		await dialog
			.getByRole("radiogroup", { name: "Level of detail" })
			.getByRole("radio", { name: "Detailed", exact: true })
			.click();
		const focus = "Use a blue color theme and highlight the 3 key stats";
		await dialog.locator("#studio-focus").fill(focus);
		await expectNoOverflow(page, "infographic dialog with choices");

		const generate = page.waitForRequest(
			(r) => r.url().endsWith("/studio/generate") && r.method() === "POST"
		);
		await dialog.getByRole("button", { name: "Generate now" }).click();
		const body = (await generate).postDataJSON() as Record<string, unknown>;
		expect(body).toEqual({
			kind: "infographic",
			title: "Infographic: E2E notebook",
			request_id: expect.stringMatching(/^[A-Za-z0-9_-]+$/),
			orientation: "square",
			style: "bento-grid",
			detail: "detailed",
			focus,
		});
	});

	test("reports: interactive format hides the file type, templates are editable", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await addNoteSource(request, token, workspace.id, notebook.id, "Report source", "Enzymes.");
		await openNotebook(page, workspace.id, notebook.id, "Studio");
		await studioTile(page, "Reports").click();
		const dialog = page.getByRole("dialog", { name: "Create report" });
		await expect(dialog.getByRole("radiogroup", { name: "File type" })).toBeVisible();
		// Templates are per format; the first one for the format is preselected.
		const editButtons = dialog.getByRole("button", { name: /^Edit / });
		await expect(editButtons.first()).toBeVisible();
		await editButtons.first().click();
		await expect(dialog.getByRole("textbox", { name: "Template instructions" })).toBeVisible();
		await expectNoOverflow(page, "report template editor");
		await dialog
			.getByRole("group", { name: "Format" })
			.getByRole("button", { name: /^Interactive/ })
			.click();
		await expect(dialog.getByRole("radiogroup", { name: "File type" })).toBeHidden();
		await expect(editButtons.first()).toBeVisible();
	});

	test("the source picker narrows what a generation uses", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await addNoteSource(request, token, workspace.id, notebook.id, "Source one", "One.");
		const two = await addNoteSource(
			request,
			token,
			workspace.id,
			notebook.id,
			"Source two",
			"Two."
		);
		await openNotebook(page, workspace.id, notebook.id, "Studio");
		await studioTile(page, "Mind Map").click();
		const dialog = page.getByRole("dialog", { name: "Customize mind map" });
		await dialog.getByRole("button", { name: "2 sources" }).click();
		// The picker view names the dialog (it replaces the options view).
		const picker = page.getByRole("dialog", { name: "Sources" });
		await expect(picker).toBeVisible();
		await expectNoOverflow(page, "studio source picker");
		await picker.getByRole("checkbox", { name: /Source one/ }).click();
		await picker.getByRole("button", { name: "Confirm" }).click();
		await expect(dialog.getByRole("button", { name: "1 source" })).toBeVisible();
		const generate = page.waitForRequest((r) => r.url().endsWith("/studio/generate"));
		await dialog.getByRole("button", { name: "Generate now" }).click();
		const body = (await generate).postDataJSON() as { source_ids?: number[] };
		expect(body.source_ids).toEqual([two.id]);
	});
});
