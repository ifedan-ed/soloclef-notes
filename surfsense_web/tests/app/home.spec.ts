import { addNoteSource, createNote, createNotebook, listNotebooks } from "../helpers/api/notebooks";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, test } from "./fixtures";

/** Notebooks home: /dashboard/<ws>/notebooks. */

const home = (ws: number) => `/dashboard/${ws}/notebooks`;
const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
/** A notebook card by exact title ("X" does not match "X (copy)"). */
const card = (page: import("@playwright/test").Page, title: string) =>
	page
		.locator("div.group")
		.filter({ has: page.getByRole("link", { name: new RegExp(`${escapeRe(title)}(?! \\()`) }) })
		.last();

test.describe("Notebooks home", () => {
	test("empty workspace shows the create tile and fits the screen", async ({ page, workspace }) => {
		await page.goto(home(workspace.id));
		await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible();
		await expect(page.getByText("No notebooks yet.", { exact: false })).toBeVisible();
		await expect(page.getByRole("radio", { name: "All" })).toHaveAttribute("aria-checked", "true");
		await expectNoOverflow(page, "notebooks home (empty)");
	});

	test("/dashboard lands on a workspace's notebooks home", async ({ page }) => {
		await page.goto("/dashboard");
		await expect(page).toHaveURL(/\/dashboard\/\d+\/notebooks$/);
		await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible();
	});

	test("create a notebook opens it", async ({ page, workspace, request, token }) => {
		await page.goto(home(workspace.id));
		await page.getByRole("button", { name: "New notebook" }).click();
		await expect(page).toHaveURL(new RegExp(`/dashboard/${workspace.id}/notebooks/\\d+`));
		await expect(page.getByRole("textbox", { name: "Notebook title" }).first()).toHaveValue(
			"Untitled notebook"
		);
		const rows = await listNotebooks(request, token, workspace.id);
		expect(rows.map((r) => r.title)).toContain("Untitled notebook");
	});

	test("search, All/Recent, rename, feature, copy and delete", async ({
		page,
		workspace,
		request,
		token,
	}) => {
		await createNotebook(request, token, workspace.id, "Alpha biology");
		await createNotebook(request, token, workspace.id, "Beta chemistry");
		await page.goto(home(workspace.id));
		await expect(card(page, "Alpha biology")).toBeVisible();
		await expect(card(page, "Beta chemistry")).toBeVisible();
		await expectNoOverflow(page, "notebooks home (two notebooks)");

		// Search filters client-side.
		const search = page.getByRole("textbox", { name: "Search notebooks" });
		await search.fill("chem");
		await expect(card(page, "Beta chemistry")).toBeVisible();
		await expect(card(page, "Alpha biology")).toBeHidden();
		await search.fill("zzz-nothing");
		await expect(page.getByText(/No notebooks match/)).toBeVisible();
		await search.fill("");

		// Recent tab.
		await page.getByRole("radio", { name: "Recent" }).click();
		await expect(page.getByRole("radio", { name: "Recent" })).toHaveAttribute(
			"aria-checked",
			"true"
		);
		await expect(card(page, "Alpha biology")).toBeVisible();
		await page.getByRole("radio", { name: "All" }).click();

		// Rename inline.
		await card(page, "Alpha biology").getByRole("button", { name: "More actions" }).click();
		await expect(page.getByRole("menu")).toBeVisible();
		await expectNoOverflow(page, "notebook card menu");
		await page.getByRole("menuitem", { name: "Rename" }).click();
		const rename = page.getByRole("textbox", { name: "Rename" });
		await expect(rename).toBeVisible();
		await expectNoOverflow(page, "inline rename");
		await rename.fill("Alpha botany");
		await page.getByRole("button", { name: "Save" }).click();
		await expect(card(page, "Alpha botany")).toBeVisible();
		await expect
			.poll(async () => (await listNotebooks(request, token, workspace.id)).map((r) => r.title))
			.toContain("Alpha botany");

		// Pin to featured → a Featured section appears.
		await card(page, "Alpha botany").getByRole("button", { name: "More actions" }).click();
		await page.getByRole("menuitem", { name: "Pin to featured" }).click();
		await expect(page.getByRole("heading", { name: "Featured notebooks" })).toBeVisible();
		await expectNoOverflow(page, "home with featured row");
		await card(page, "Alpha botany").getByRole("button", { name: "More actions" }).click();
		await page.getByRole("menuitem", { name: "Remove from featured" }).click();
		await expect(page.getByRole("heading", { name: "Featured notebooks" })).toBeHidden();

		// Copy → navigates to the copy.
		await card(page, "Beta chemistry").getByRole("button", { name: "More actions" }).click();
		await page.getByRole("menuitem", { name: "Copy notebook" }).click();
		await expect(page).toHaveURL(/\/notebooks\/\d+/);
		await expect(page.getByRole("textbox", { name: "Notebook title" }).first()).toHaveValue(
			"Beta chemistry (copy)"
		);

		// Delete with the inline confirm.
		await page.goto(home(workspace.id));
		await card(page, "Beta chemistry (copy)").getByRole("button", { name: "More actions" }).click();
		await page.getByRole("menuitem", { name: "Delete" }).click();
		const confirm = page.getByRole("alert").filter({ hasText: "Delete “Beta chemistry (copy)”?" });
		await expect(confirm).toBeVisible();
		await expectNoOverflow(page, "inline delete confirm");
		await confirm.getByRole("button", { name: "Delete" }).click();
		await expect
			.poll(async () => (await listNotebooks(request, token, workspace.id)).map((r) => r.title))
			.not.toContain("Beta chemistry (copy)");
		await expect(card(page, "Beta chemistry (copy)")).toBeHidden();
	});

	test("search looks inside sources and notes, and opens what it found", async ({
		page,
		workspace,
		request,
		token,
		isPhone,
	}) => {
		// A made-up word, so only these rows can match.
		const word = "quokkaspacer";
		const lungs = await createNotebook(request, token, workspace.id, "Lung clinic", "🩺");
		await addNoteSource(
			request,
			token,
			workspace.id,
			lungs.id,
			"Spacer technique",
			`Children should use a ${word} with every inhaled corticosteroid dose. Rinse the mouth afterwards.`
		);
		const pharmacy = await createNotebook(request, token, workspace.id, "Pharmacy", "💊");
		await createNote(
			request,
			token,
			workspace.id,
			pharmacy.id,
			"Dosing chart",
			`Stock one ${word} per ward and check the valve weekly.`
		);
		await createNotebook(request, token, workspace.id, "Unrelated", "📓");

		await page.goto(home(workspace.id));
		await expect(card(page, "Unrelated")).toBeVisible();
		const search = page.getByRole("textbox", { name: "Search notebooks" });

		// One character: titles filter, nothing is searched inside yet.
		await search.fill("q");
		await expect(page.getByRole("heading", { name: "Found inside notebooks" })).toBeHidden();

		await search.fill(word);
		// No title matches, but the content search finds both notebooks.
		await expect(page.getByText(/No notebooks match/)).toBeVisible();
		const found = page.locator("section[aria-labelledby='content-search-title']");
		await expect(found.getByRole("heading", { name: "Found inside notebooks" })).toBeVisible();
		await expect(found.getByRole("article", { name: "Lung clinic" })).toBeVisible();
		await expect(found.getByRole("article", { name: "Pharmacy" })).toBeVisible();
		await expect(found.getByRole("article", { name: "Unrelated" })).toHaveCount(0);
		await expect(found.locator("mark", { hasText: word }).first()).toBeVisible();
		const sourceHit = found
			.getByRole("article", { name: "Lung clinic" })
			.getByRole("link", { name: /Spacer technique/ })
			.first();
		await expect(sourceHit).toHaveAttribute("href", /\?(source|note)=\d+$/);
		const noteHit = found
			.getByRole("article", { name: "Pharmacy" })
			.getByRole("link", { name: /Dosing chart/ });
		await expect(noteHit).toHaveAttribute("href", /\?note=\d+$/);
		await expectNoOverflow(page, "home search inside notebooks");

		// Keyboard: Down from the box walks the results, Escape comes back.
		await search.focus();
		await search.press("ArrowDown");
		const results = found.locator("[data-search-result]");
		await expect(results.first()).toBeFocused();
		await page.keyboard.press("ArrowDown");
		await expect(results.nth(1)).toBeFocused();
		await page.keyboard.press("Escape");
		await expect(search).toBeFocused();

		// Refining keeps the earlier results painted until the new ones land.
		await search.fill(`${word} valve`);
		await expect(found.getByRole("article").first()).toBeVisible();
		await expect(found.getByRole("article", { name: "Pharmacy" })).toBeVisible();

		// Nothing matches: say so.
		await search.fill("zzqqxx-nothing-here");
		await expect(found.getByText(/Nothing inside your notebooks matches/)).toBeVisible();
		await expectNoOverflow(page, "home search, no content matches");

		// A note hit opens the notebook with that note open.
		await search.fill(word);
		await noteHit.click();
		// The focus parameter is consumed; the URL never names the chat.
		await expect(page).toHaveURL(new RegExp(`/notebooks/${pharmacy.id}$`));
		await expect(page.getByRole("textbox", { name: "Note title" })).toHaveValue("Dosing chart");

		// A source hit opens the notebook with its source guide open.
		await page.goto(home(workspace.id));
		await page.getByRole("textbox", { name: "Search notebooks" }).fill(word);
		const docHit = page
			.locator("section[aria-labelledby='content-search-title']")
			.getByRole("article", { name: "Lung clinic" })
			.locator("a[href*='?source=']")
			.first();
		await docHit.click();
		// The focus parameter is consumed; the URL never names the chat.
		await expect(page).toHaveURL(new RegExp(`/notebooks/${lungs.id}$`));
		await expect(page.getByText("Source guide", { exact: true })).toBeVisible();
		await expect(page.getByRole("button", { name: "Back to sources" })).toBeVisible();
		if (isPhone) {
			await expect(page.getByRole("tab", { name: /Sources/ })).toHaveAttribute(
				"aria-selected",
				"true"
			);
		}
		await expectNoOverflow(page, "notebook opened on a source from search");
	});
});
