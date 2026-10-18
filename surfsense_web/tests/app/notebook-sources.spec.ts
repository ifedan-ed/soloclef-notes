import path from "node:path";
import { addNoteSource, createNotebook, getNotebook, listSources } from "../helpers/api/notebooks";
import { expectHeaderClearsSafeArea, expectNoOverflow } from "../helpers/layout/overflow";
import { send } from "../helpers/ui/chat";
import { dropFiles } from "../helpers/ui/drop";
import { expect, openNotebook, showPanel, test } from "./fixtures";

/** Notebook page layout and the Sources panel. */

const FIXTURES = path.join(__dirname, "..", "fixtures-files");

test.describe("Notebook page", () => {
	test("layout: header, panels / phone tabs, safe area", async ({
		page,
		workspace,
		notebook,
		isPhone,
	}) => {
		await openNotebook(page, workspace.id, notebook.id);
		const header = page.locator("header").filter({
			has: page.getByRole("textbox", { name: "Notebook title" }),
		});
		await expect(header).toBeVisible();
		await expect(page.getByRole("link", { name: "Back to notebooks" })).toBeVisible();
		await expectNoOverflow(page, "notebook page (chat)");

		if (isPhone) {
			await expectHeaderClearsSafeArea(header);
			const tabs = page
				.getByRole("tablist")
				.filter({ has: page.getByRole("tab", { name: "Chat" }) });
			await expect(tabs.getByRole("tab", { name: "Chat" })).toHaveAttribute(
				"aria-selected",
				"true"
			);
			await expect(tabs.getByRole("tab", { name: /^Sources \(0\)/ })).toBeVisible();
			for (const tab of ["Sources", "Studio", "Chat"] as const) {
				await showPanel(page, tab);
				await expectNoOverflow(page, `notebook phone tab ${tab}`);
			}
		} else {
			await expect(page.getByRole("heading", { name: "Sources", level: 2 })).toBeVisible();
			await expect(page.getByRole("heading", { name: "Studio", level: 2 })).toBeVisible();
			await page.getByRole("button", { name: "Collapse sources" }).click();
			await expect(page.getByRole("button", { name: "Expand sources" })).toBeVisible();
			await page.getByRole("button", { name: "Expand sources" }).click();
			await page.getByRole("button", { name: "Collapse studio" }).click();
			await expect(page.getByRole("button", { name: "Expand studio" })).toBeVisible();
			await page.getByRole("button", { name: "Expand studio" }).click();
			await expectNoOverflow(page, "notebook desktop panels");
		}
	});

	test("rename in the header persists", async ({ page, workspace, notebook, request, token }) => {
		await openNotebook(page, workspace.id, notebook.id);
		const title = page.getByRole("textbox", { name: "Notebook title" }).first();
		await title.fill("Renamed from header");
		await title.press("Enter");
		await expect
			.poll(async () => {
				const r = await request.get(`/api/v1/workspaces/${workspace.id}/notebooks/${notebook.id}`, {
					headers: { Authorization: `Bearer ${token}` },
				});
				return ((await r.json()) as { title: string }).title;
			})
			.toBe("Renamed from header");
	});

	test("back link returns to the notebooks home", async ({ page, workspace, notebook }) => {
		await openNotebook(page, workspace.id, notebook.id);
		await page.getByRole("link", { name: "Back to notebooks" }).click();
		await expect(page).toHaveURL(new RegExp(`/dashboard/${workspace.id}/notebooks$`));
	});
});

test.describe("Sources panel", () => {
	test("empty state and the Add sources dialog tabs", async ({ page, workspace, notebook }) => {
		await openNotebook(page, workspace.id, notebook.id, "Sources");
		await expect(page.getByText("Saved sources will appear here")).toBeVisible();
		await expectNoOverflow(page, "sources panel (empty)");

		await page.getByRole("button", { name: "Add sources" }).first().click();
		const dialog = page.getByRole("dialog", { name: "Add sources" });
		await expect(dialog).toBeVisible();
		await expect(
			dialog.getByRole("textbox", { name: "Search documents and folders" })
		).toBeVisible();
		// Documents and folders only: a connector account is not a source.
		await expect(dialog.getByText("Choose the exact account for tool use")).toHaveCount(0);
		await expect(dialog.getByText("Browse your knowledge base")).toHaveCount(0);
		await expectNoOverflow(page, "Add sources dialog: workspace tab");

		await dialog.getByRole("tab", { name: /Upload/ }).click();
		await expect(dialog.getByRole("tab", { name: /Upload/ })).toHaveAttribute(
			"aria-selected",
			"true"
		);
		await expectNoOverflow(page, "Add sources dialog: upload tab");

		await dialog.getByRole("tab", { name: /connector/i }).click();
		await expect(dialog.getByText("Files from your connected apps")).toBeVisible();
		await expect(dialog.getByRole("link", { name: "Manage connectors" })).toBeVisible();
		await expectNoOverflow(page, "Add sources dialog: connector tab");

		await page.keyboard.press("Escape");
		await expect(dialog).toBeHidden();
	});

	test("upload a markdown file with the Local parser adds a source", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await openNotebook(page, workspace.id, notebook.id, "Sources");
		await page.getByRole("button", { name: "Add sources" }).first().click();
		const dialog = page.getByRole("dialog", { name: "Add sources" });
		await dialog.getByRole("tab", { name: /Upload/ }).click();
		await dialog
			.locator('input[type="file"]')
			.first()
			.setInputFiles(path.join(FIXTURES, "e2e-upload.md"));
		await expect(dialog.getByText("e2e-upload.md")).toBeVisible();
		await expectNoOverflow(page, "upload tab with a file");
		const local = dialog.getByRole("button", { name: /^Local/ });
		if (await local.isVisible()) {
			await local.click();
			await expect(local).toHaveAttribute("aria-pressed", "true");
		}
		await dialog.getByRole("button", { name: /^Upload 1 file/ }).click();
		await expect
			.poll(
				async () =>
					(await listSources(request, token, workspace.id, notebook.id)).map((s) => s.title),
				{
					timeout: 60_000,
				}
			)
			.toContainEqual(expect.stringContaining("e2e-upload"));
	});

	test("dropping a file asks for the parser and vision before uploading", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		// Regression: a drop uploaded at once with the default parser and no
		// vision, skipping the choices Add sources → Upload offers.
		await openNotebook(page, workspace.id, notebook.id, "Sources");
		await dropFiles(page, page.getByText("Saved sources will appear here"), [
			{ name: "dropped-notes.md", type: "text/markdown", text: "# Dropped\n\nDropped notes." },
		]);

		const dialog = page.getByRole("dialog", { name: "Add sources" });
		await expect(dialog).toBeVisible();
		await expect(dialog.getByRole("tab", { name: /Upload/ })).toHaveAttribute(
			"aria-selected",
			"true"
		);
		await expect(dialog.getByText("dropped-notes.md")).toBeVisible();
		// Same defaults as the Upload tab: vision off.
		await expect(dialog.getByRole("switch")).toHaveAttribute("aria-checked", "false");
		await expectNoOverflow(page, "upload tab with a dropped file");
		// Nothing was uploaded or linked by the drop itself.
		expect(await listSources(request, token, workspace.id, notebook.id)).toEqual([]);

		const local = dialog.getByRole("button", { name: /^Local/ });
		if (await local.isVisible()) {
			await local.click();
			await expect(local).toHaveAttribute("aria-pressed", "true");
		}
		await dialog.getByRole("button", { name: /^Upload 1 file/ }).click();
		await expect
			.poll(
				async () =>
					(await listSources(request, token, workspace.id, notebook.id)).map((s) => s.title),
				{ timeout: 60_000 }
			)
			.toContainEqual(expect.stringContaining("dropped-notes"));
		await expect(dialog).toBeHidden();
	});

	test("cancelling a dropped file uploads nothing", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await openNotebook(page, workspace.id, notebook.id, "Sources");
		await dropFiles(page, page.getByText("Saved sources will appear here"), [
			{ name: "never-sent.md", type: "text/markdown", text: "never sent" },
		]);
		const dialog = page.getByRole("dialog", { name: "Add sources" });
		await expect(dialog.getByText("never-sent.md")).toBeVisible();
		await page.keyboard.press("Escape");
		await expect(dialog).toBeHidden();
		expect(await listSources(request, token, workspace.id, notebook.id)).toEqual([]);

		// Add sources opened by hand starts clean, not with the old drop.
		await page.getByRole("button", { name: "Add sources" }).first().click();
		await dialog.getByRole("tab", { name: /Upload/ }).click();
		await expect(dialog.getByText("never-sent.md")).toHaveCount(0);
	});

	test("sources list: toggle, select all, guide, remove", async ({
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
			"Cell biology notes",
			"Mitochondria make ATP."
		);
		await addNoteSource(
			request,
			token,
			workspace.id,
			notebook.id,
			"Genetics notes",
			"DNA is a double helix."
		);
		await openNotebook(page, workspace.id, notebook.id, "Sources");

		const use = (title: string) => page.getByRole("checkbox", { name: `Use ${title}` });
		await expect(use("Cell biology notes")).toBeChecked();
		await expect(use("Genetics notes")).toBeChecked();
		await expectNoOverflow(page, "sources list");

		// Untick one → Select all goes indeterminate and the API agrees.
		await use("Genetics notes").click();
		await expect(use("Genetics notes")).not.toBeChecked();
		await expect(page.getByRole("checkbox", { name: "Select all sources" })).toHaveAttribute(
			"aria-checked",
			"mixed"
		);
		await expect
			.poll(
				async () =>
					(await listSources(request, token, workspace.id, notebook.id)).find(
						(s) => s.title === "Genetics notes"
					)?.enabled
			)
			.toBe(false);

		// Select all → both on.
		await page.getByRole("checkbox", { name: "Select all sources" }).click();
		await expect(use("Genetics notes")).toBeChecked();

		// Source guide.
		await page.getByRole("button", { name: "Cell biology notes", exact: true }).click();
		await expect(page.getByText("Source guide")).toBeVisible();
		await expect(page.getByText(/E2E source guide/)).toBeVisible({ timeout: 30_000 });
		await expectNoOverflow(page, "source guide");
		await page.getByRole("button", { name: "Back to sources" }).click();

		// Remove from notebook.
		const row = page
			.locator("li, div")
			.filter({ has: use("Genetics notes") })
			.last();
		await row.hover();
		await row.getByRole("button", { name: "More actions" }).click();
		await expectNoOverflow(page, "source row menu");
		await page.getByRole("menuitem", { name: "Remove from notebook" }).click();
		await expect(use("Genetics notes")).toBeHidden();
		await expect
			.poll(async () =>
				(await listSources(request, token, workspace.id, notebook.id)).map((s) => s.title)
			)
			.toEqual(["Cell biology notes"]);
	});

	test("the workspace tab links an existing document", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		// A document that exists in the workspace but not in this notebook: add
		// it as a source of a second notebook, then pick it from this one.
		const other = await request.post(`/api/v1/workspaces/${workspace.id}/notebooks`, {
			headers: { Authorization: `Bearer ${token}` },
			data: { title: "Other notebook" },
		});
		const otherId = ((await other.json()) as { id: number }).id;
		await addNoteSource(
			request,
			token,
			workspace.id,
			otherId,
			"Shared ecology notes",
			"Food webs."
		);

		await openNotebook(page, workspace.id, notebook.id, "Sources");
		await page.getByRole("button", { name: "Add sources" }).first().click();
		const dialog = page.getByRole("dialog", { name: "Add sources" });
		await dialog.getByRole("textbox", { name: "Search documents and folders" }).fill("ecology");
		await dialog.getByText("Shared ecology notes").first().click();
		await expectNoOverflow(page, "workspace tab with a pick");
		await dialog.getByRole("button", { name: /^Add 1 source/ }).click();
		await expect(dialog).toBeHidden();
		await expect(page.getByRole("checkbox", { name: "Use Shared ecology notes" })).toBeVisible();
	});
});

/**
 * An "Untitled notebook" names itself (the fake quick-tasks model answers
 * "E2E Named Notebook" with 🔬) and the page shows it without a reload; a
 * rename by the user stops that for good.
 */
test.describe("Automatic notebook name", () => {
	const NAMED = "E2E Named Notebook";

	test("the first ready source names it; a rename stops automatic naming", async ({
		page,
		workspace,
		request,
		token,
	}) => {
		const notebook = await createNotebook(request, token, workspace.id, "Untitled notebook");
		await openNotebook(page, workspace.id, notebook.id, "Sources");
		const title = page.getByRole("textbox", { name: "Notebook title" }).first();
		await expect(title).toHaveValue("Untitled notebook");

		await page.getByRole("button", { name: "Add sources" }).first().click();
		const dialog = page.getByRole("dialog", { name: "Add sources" });
		await dialog.getByRole("tab", { name: /Upload/ }).click();
		await dialog
			.locator('input[type="file"]')
			.first()
			.setInputFiles(path.join(FIXTURES, "e2e-upload.md"));
		const local = dialog.getByRole("button", { name: /^Local/ });
		if (await local.isVisible()) await local.click();
		await dialog.getByRole("button", { name: /^Upload 1 file/ }).click();

		// No reload: the page notices the source became ready and picks up the name.
		await expect(title).toHaveValue(NAMED, { timeout: 90_000 });
		await expect(page.getByRole("button", { name: "Change icon" }).first()).toHaveText("🔬");
		expect((await getNotebook(request, token, workspace.id, notebook.id)).title_source).toBe(
			"auto"
		);

		// The user renames it: from now on it keeps the user's name.
		await title.fill("My premature birth notes");
		await title.press("Enter");
		await expect
			.poll(async () => (await getNotebook(request, token, workspace.id, notebook.id)).title_source)
			.toBe("user");
		await addNoteSource(
			request,
			token,
			workspace.id,
			notebook.id,
			"Later notes",
			"More material after the rename."
		);
		// The naming task waits a few seconds after a source is ready; give it time.
		await page.waitForTimeout(8_000);
		const after = await getNotebook(request, token, workspace.id, notebook.id);
		expect(after.title).toBe("My premature birth notes");
		await expect(title).toHaveValue("My premature birth notes");

		// Home shows the user's name too.
		await page.getByRole("link", { name: "Back to notebooks" }).click();
		await expect(
			page.getByRole("link", { name: /My premature birth notes/ }).first()
		).toBeVisible();
	});

	test("with no source yet, the first question names it", async ({
		page,
		workspace,
		request,
		token,
	}) => {
		const notebook = await createNotebook(request, token, workspace.id, "Untitled notebook");
		await openNotebook(page, workspace.id, notebook.id, "Chat");
		const title = page.getByRole("textbox", { name: "Notebook title" }).first();
		await expect(title).toHaveValue("Untitled notebook");

		await send(page, "E2E_ANSWER what respiratory problems follow premature birth?");
		await expect(title).toHaveValue(NAMED, { timeout: 60_000 });
		expect((await getNotebook(request, token, workspace.id, notebook.id)).title_source).toBe(
			"auto"
		);
	});
});
