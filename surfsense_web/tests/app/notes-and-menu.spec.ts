import { authHeaders } from "../helpers/api/auth";
import { getNotebook, listNotebooks, listNotes, listSources } from "../helpers/api/notebooks";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, openNotebook, test } from "./fixtures";

/** Studio notes, and the notebook ⋮ menu's dialogs. */

test.describe("Notes", () => {
	test("add, edit (autosave), convert to source, delete", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await openNotebook(page, workspace.id, notebook.id, "Studio");
		await page.getByRole("button", { name: "Add note" }).click();
		const title = page.getByRole("textbox", { name: "Note title" });
		await expect(title).toBeVisible();
		await expectNoOverflow(page, "note editor");

		await title.fill("E2E lab note");
		const body = page
			.locator('[contenteditable="true"]')
			.filter({ hasNotText: "Ask anything" })
			.last();
		await body.click();
		await body.pressSequentially("Chlorophyll absorbs red and blue light.");
		await expect
			.poll(
				async () =>
					(await listNotes(request, token, workspace.id, notebook.id)).map((n) => n.title),
				{
					timeout: 10_000,
				}
			)
			.toContain("E2E lab note");
		await expect
			.poll(
				async () => (await listNotes(request, token, workspace.id, notebook.id))[0]?.content ?? ""
			)
			.toContain("Chlorophyll");

		await page.getByRole("button", { name: "Convert to source" }).click();
		await expect(page.getByText("The note is now a source.")).toBeVisible();
		await expect(page.getByRole("button", { name: "Added as a source" })).toBeDisabled();
		await expect
			.poll(async () =>
				(await listSources(request, token, workspace.id, notebook.id)).map((s) => s.title)
			)
			.toContain("E2E lab note");

		await page.getByRole("button", { name: "Delete", exact: true }).click();
		const confirm = page.getByRole("alert").filter({ hasText: "Delete this note?" });
		await expect(confirm).toBeVisible();
		await expectNoOverflow(page, "note delete confirm");
		await confirm.getByRole("button", { name: "Delete" }).click();
		await expect
			.poll(async () => (await listNotes(request, token, workspace.id, notebook.id)).length)
			.toBe(0);
		await expect(page.getByRole("button", { name: "Add note" })).toBeVisible();
	});

	test("Back to studio keeps the edit and lists the note", async ({
		page,
		workspace,
		notebook,
	}) => {
		await openNotebook(page, workspace.id, notebook.id, "Studio");
		await page.getByRole("button", { name: "Add note" }).click();
		await page.getByRole("textbox", { name: "Note title" }).fill("E2E quick note");
		await page.getByRole("button", { name: "Back to studio" }).click();
		await expect(page.getByRole("button", { name: /^E2E quick note/ })).toBeVisible();
	});
});

test.describe("Notebook menu", () => {
	test("customize notebook: title and emoji", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await openNotebook(page, workspace.id, notebook.id);
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
		const saved = await getNotebook(request, token, workspace.id, notebook.id);
		expect(saved.title).toBe("Customized notebook");
		expect(saved.icon).not.toBe("📓");
	});

	test("configure chat: goal and response length", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await openNotebook(page, workspace.id, notebook.id);
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
		const saved = await getNotebook(request, token, workspace.id, notebook.id);
		expect(saved).toMatchObject({
			chat_goal: "custom",
			chat_instructions: "Answer like a biology tutor.",
			response_length: "shorter",
		});
	});

	test("copy and pin from the menu", async ({ page, workspace, notebook, request, token }) => {
		await openNotebook(page, workspace.id, notebook.id);
		await page.getByRole("button", { name: "Notebook options" }).click();
		await page.getByRole("menuitem", { name: "Pin to featured" }).click();
		await expect
			.poll(async () => (await getNotebook(request, token, workspace.id, notebook.id)).featured)
			.toBe(true);

		await page.getByRole("button", { name: "Notebook options" }).click();
		await expect(page.getByRole("menuitem", { name: "Remove from featured" })).toBeVisible();
		await page.getByRole("menuitem", { name: "Copy notebook" }).click();
		await expect(page.getByText("Notebook copied with its sources and notes.")).toBeVisible();
		await expect(page).not.toHaveURL(new RegExp(`/notebooks/${notebook.id}(\\?|$)`));
		const titles = (await listNotebooks(request, token, workspace.id)).map((n) => n.title);
		expect(titles.filter((t) => t.startsWith("E2E notebook"))).toHaveLength(2);
	});

	test("header Create notebook opens a new one", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await openNotebook(page, workspace.id, notebook.id);
		await page.getByRole("button", { name: "Create notebook" }).click();
		await expect(page).not.toHaveURL(new RegExp(`/notebooks/${notebook.id}(\\?|$)`));
		await expect(page.getByRole("textbox", { name: "Notebook title" }).first()).toHaveValue(
			"Untitled notebook"
		);
		const res = await request.get(`/api/v1/workspaces/${workspace.id}/notebooks`, {
			headers: authHeaders(token),
		});
		expect(((await res.json()) as unknown[]).length).toBe(2);
	});
});
