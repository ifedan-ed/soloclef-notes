import { authHeaders } from "../helpers/api/auth";
import { deleteNotebook, getNotebook, listNotebooks } from "../helpers/api/notebooks";
import { getNote, type NoteMaker, notesIn, provideNotes } from "../helpers/api/notes";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { noteCreated, openNote, openNotesTab } from "../helpers/ui/notes";
import { test as base, expect, openNotebook } from "./fixtures";

/** A notebook's Notes tab, and the notebook ⋮ menu's dialogs. */

const test = base.extend<{ notes: NoteMaker }>({
	notes: async ({ request, token }, use) => provideNotes(request, token, use),
});

test.describe("A notebook's notes", () => {
	test("write one here (autosave), make it a source, delete it", async ({
		page,
		notebook,
		request,
		token,
		notes,
	}) => {
		await openNotesTab(page, notebook.id);
		await expect(page.getByText("No notes in this notebook yet")).toBeVisible();
		const created = noteCreated(page);
		await page.getByRole("button", { name: "New note in this notebook" }).click();
		const { id } = (await (await created).json()) as { id: number };
		notes.track(id);

		const view = openNote(page);
		const title = view.getByRole("textbox", { name: "Note title" });
		await expect(title).toBeVisible();
		await expectNoOverflow(page, "note editor");
		await title.fill("E2E lab note");
		const body = view.locator("[data-slate-editor]");
		await body.click();
		await body.pressSequentially("Chlorophyll absorbs red and blue light.");
		// Saved without a Save button, filed in this notebook.
		await expect
			.poll(async () => (await getNote(request, token, id)).content, { timeout: 10_000 })
			.toContain("Chlorophyll");
		const saved = await getNote(request, token, id);
		expect(saved.title).toBe("E2E lab note");
		expect(saved.notebook?.id).toBe(notebook.id);

		// Back to the list: the note is there, with its source switch.
		await page.getByRole("button", { name: "Back to this notebook's notes" }).click();
		await expect(page.getByRole("button", { name: /^E2E lab note/ })).toBeVisible();
		const source = page.getByRole("switch", { name: "Let this notebook's chat read the note" });
		await expect(source).not.toBeChecked();
		await source.click();
		await expect(source).toBeChecked();
		// The note itself is the source, not a copy of it.
		await expect
			.poll(async () => (await getNote(request, token, id)).sources.map((s) => s.id))
			.toEqual([notebook.id]);

		// The note's row in the list, by its title and first words: switched
		// on, the note is also a row of the Sources panel, under its title alone.
		await page.getByRole("button", { name: /^E2E lab note Chlorophyll/ }).click();
		await view.getByRole("button", { name: "More actions" }).click();
		await page.getByRole("menuitem", { name: "Delete" }).click();
		const confirm = page.getByRole("alertdialog").filter({ hasText: "Delete this note?" });
		await expect(confirm).toBeVisible();
		await expectNoOverflow(page, "note delete confirm");
		await confirm.getByRole("button", { name: "Delete" }).click();
		await expect.poll(async () => (await notesIn(request, token, notebook.id)).length).toBe(0);
		await expect(page.getByRole("button", { name: "New note in this notebook" })).toBeVisible();
	});

	test("Back keeps the edit and lists the note", async ({ page, notebook, notes }) => {
		await openNotesTab(page, notebook.id);
		const created = noteCreated(page);
		await page.getByRole("button", { name: "New note in this notebook" }).click();
		notes.track(((await (await created).json()) as { id: number }).id);
		await openNote(page).getByRole("textbox", { name: "Note title" }).fill("E2E quick note");
		// Back flushes the title still waiting for its autosave.
		await page.getByRole("button", { name: "Back to this notebook's notes" }).click();
		await expect(page.getByRole("button", { name: /^E2E quick note/ })).toBeVisible();
	});
});

test.describe("Notebook menu", () => {
	test("customize notebook: title and emoji", async ({ page, notebook, request, token }) => {
		await openNotebook(page, notebook.id);
		await page.getByRole("button", { name: "Notebook options" }).click();
		await expect(page.getByRole("menu")).toBeVisible();
		await expectNoOverflow(page, "notebook menu");
		await page.getByRole("menuitem", { name: "Customize notebook" }).click();
		const dialog = page.getByRole("dialog", { name: "Customize notebook" });
		await expect(dialog).toBeVisible();
		await expectNoOverflow(page, "customize notebook dialog");
		await dialog.getByLabel("Notebook title").fill("Customized notebook");
		await dialog.getByRole("group", { name: "Emoji" }).getByRole("button").nth(3).click();
		await dialog.getByRole("button", { name: "Save" }).click();
		await expect(dialog).toBeHidden();
		const saved = await getNotebook(request, token, notebook.id);
		expect(saved.title).toBe("Customized notebook");
		expect(saved.icon).not.toBe("📓");
	});

	test("configure chat: goal and response length", async ({ page, notebook, request, token }) => {
		await openNotebook(page, notebook.id);
		await page.getByRole("button", { name: "Notebook options" }).click();
		await page.getByRole("menuitem", { name: "Configure chat" }).click();
		const dialog = page.getByRole("dialog", { name: "Configure chat" });
		await expect(dialog).toBeVisible();
		await expectNoOverflow(page, "configure chat dialog");

		await dialog.getByRole("button", { name: /^Custom/ }).click();
		const instructions = dialog.getByRole("textbox", { name: "Custom instructions" });
		await expect(instructions).toBeVisible();
		await expect(dialog.getByRole("button", { name: "Save" })).toBeDisabled();
		await instructions.fill("Answer like a biology tutor.");
		await dialog
			.getByRole("radiogroup", { name: "Response length" })
			.getByRole("radio", { name: "Shorter" })
			.click();
		await expectNoOverflow(page, "configure chat (custom)");
		await dialog.getByRole("button", { name: "Save" }).click();
		await expect(dialog).toBeHidden();
		const saved = await getNotebook(request, token, notebook.id);
		expect(saved).toMatchObject({
			chat_goal: "custom",
			chat_instructions: "Answer like a biology tutor.",
			response_length: "shorter",
		});
	});

	test("copy and pin from the menu", async ({ page, notebook, request, token }) => {
		await openNotebook(page, notebook.id);
		await page.getByRole("button", { name: "Notebook options" }).click();
		await page.getByRole("menuitem", { name: "Pin to featured" }).click();
		await expect
			.poll(async () => (await getNotebook(request, token, notebook.id)).featured)
			.toBe(true);

		await page.getByRole("button", { name: "Notebook options" }).click();
		await expect(page.getByRole("menuitem", { name: "Remove from featured" })).toBeVisible();
		await page.getByRole("menuitem", { name: "Copy notebook" }).click();
		await expect(page.getByText("Notebook copied with its sources and notes.")).toBeVisible();
		await expect(page).not.toHaveURL(new RegExp(`/notebooks/${notebook.id}(\\?|$)`));
		const titles = (await listNotebooks(request, token)).map((n) => n.title);
		expect(titles.filter((t) => t.startsWith(notebook.title))).toHaveLength(2);
	});

	test("header Create notebook opens a new one", async ({ page, notebook, request, token }) => {
		await openNotebook(page, notebook.id);
		await page.getByRole("button", { name: "Create notebook" }).click();
		await expect(page).not.toHaveURL(new RegExp(`/notebooks/${notebook.id}(\\?|$)`));
		await expect(page.getByRole("textbox", { name: "Notebook title" }).first()).toHaveValue(
			"Untitled notebook"
		);
		// The page it opened is a notebook of its own. (The account is every
		// test's, so how many notebooks it holds says nothing; this one does.)
		await expect(page).toHaveURL(/\/dashboard\/notebooks\/\d+$/);
		const id = Number(new URL(page.url()).pathname.split("/").pop());
		expect(id).not.toBe(notebook.id);
		const res = await request.get(`/api/v1/notebooks/${id}`, { headers: authHeaders(token) });
		expect(res.ok(), await res.text()).toBeTruthy();
		expect(((await res.json()) as { title: string }).title).toBe("Untitled notebook");
		await deleteNotebook(request, token, id);
	});
});
