import type { APIRequestContext } from "@playwright/test";
import {
	addNoteSource,
	createNote,
	createNotebook,
	deleteNotebook,
	listNotebooks,
} from "../helpers/api/notebooks";
import { getNote } from "../helpers/api/notes";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, test, uniqueName } from "./fixtures";

/**
 * Notebooks home: /dashboard/notebooks, what the app opens on.
 *
 * Every test of the run signs in as the same account, and the desktop and
 * phone projects run side by side: each test names its notebooks uniquely
 * and deletes them afterwards, and never assumes the account is empty.
 */

const home = () => "/dashboard/notebooks";

/** Notebooks a test made, deleted when it ends whether it passed or not. */
function made(request: APIRequestContext, token: string) {
	const ids: number[] = [];
	return {
		track: <T extends { id: number }>(row: T) => {
			ids.push(row.id);
			return row;
		},
		cleanup: async () => {
			for (const id of ids) await deleteNotebook(request, token, id).catch(() => undefined);
		},
	};
}
const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
/** A notebook card by exact title ("X" does not match "X (copy)"). */
const card = (page: import("@playwright/test").Page, title: string) =>
	page
		.locator("div.group")
		.filter({ has: page.getByRole("link", { name: new RegExp(`${escapeRe(title)}(?! \\()`) }) })
		.last();

test.describe("Notebooks home", () => {
	test("an account with no notebooks sees the create tile, and it fits", async ({ page }) => {
		// The account is shared by the run and is never empty, so the empty
		// home is shown by answering the list with none.
		await page.route(/\/api\/v1\/notebooks(\?.*)?$/, (route) =>
			route.request().method() === "GET" ? route.fulfill({ json: [] }) : route.continue()
		);
		await page.goto(home());
		await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible();
		await expect(page.getByText("No notebooks yet.", { exact: false })).toBeVisible();
		await expect(page.getByRole("radio", { name: "All" })).toHaveAttribute("aria-checked", "true");
		await expectNoOverflow(page, "notebooks home (empty)");
	});

	test("/dashboard lands on the notebooks home, with nothing to choose", async ({ page }) => {
		await page.goto("/dashboard");
		await expect(page).toHaveURL(/\/dashboard\/notebooks$/);
		await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible();
		// No workspace switcher or "Add workspace": the account is the only scope.
		await expect(page.getByRole("button", { name: /add workspace/i })).toHaveCount(0);
	});

	test("a new account chats on the shared models: no onboarding on the way home", async ({
		page,
	}) => {
		const visited: string[] = [];
		page.on("framenavigated", (frame) => {
			if (frame === page.mainFrame()) visited.push(frame.url());
		});
		await page.goto("/dashboard");
		await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible();
		expect(visited.filter((url) => url.includes("/onboard"))).toEqual([]);
	});

	test("create a notebook opens it", async ({ page, request, token }) => {
		await page.goto(home());
		await page.getByRole("button", { name: "New notebook" }).click();
		await expect(page).toHaveURL(/\/dashboard\/notebooks\/\d+/);
		const id = Number(new URL(page.url()).pathname.split("/").pop());
		try {
			await expect(page.getByRole("textbox", { name: "Notebook title" }).first()).toHaveValue(
				"Untitled notebook"
			);
			const rows = await listNotebooks(request, token);
			expect(rows.find((r) => r.id === id)?.title).toBe("Untitled notebook");
		} finally {
			await deleteNotebook(request, token, id).catch(() => undefined);
		}
	});

	test("search, All/Recent, rename, feature, copy and delete", async ({ page, request, token }) => {
		// One run-unique tag, so the other project's copy of this test (same
		// account, same moment) has cards of its own.
		const tag = uniqueName("e2e").slice(4);
		const alpha = `Alpha biology ${tag}`;
		const botany = `Alpha botany ${tag}`;
		const beta = `Beta chemistry ${tag}`;
		const copied = `${beta} (copy)`;
		const notebooks = made(request, token);
		try {
			notebooks.track(await createNotebook(request, token, alpha));
			notebooks.track(await createNotebook(request, token, beta));
			await page.goto(home());
			await expect(card(page, alpha)).toBeVisible();
			await expect(card(page, beta)).toBeVisible();
			await expectNoOverflow(page, "notebooks home (two notebooks)");

			// Search filters client-side.
			const search = page.getByRole("textbox", { name: "Search notebooks" });
			await search.fill(tag);
			await expect(card(page, alpha)).toBeVisible();
			await search.fill(`chemistry ${tag}`);
			await expect(card(page, beta)).toBeVisible();
			await expect(card(page, alpha)).toBeHidden();
			await search.fill("zzz-nothing");
			await expect(page.getByText(/No notebooks match/)).toBeVisible();
			await search.fill("");

			// Recent tab.
			await page.getByRole("radio", { name: "Recent" }).click();
			await expect(page.getByRole("radio", { name: "Recent" })).toHaveAttribute(
				"aria-checked",
				"true"
			);
			await expect(card(page, alpha)).toBeVisible();
			await page.getByRole("radio", { name: "All" }).click();

			// Rename inline.
			await card(page, alpha).getByRole("button", { name: "More actions" }).click();
			await expect(page.getByRole("menu")).toBeVisible();
			await expectNoOverflow(page, "notebook card menu");
			await page.getByRole("menuitem", { name: "Rename" }).click();
			const rename = page.getByRole("textbox", { name: "Rename" });
			await expect(rename).toBeVisible();
			await expectNoOverflow(page, "inline rename");
			await rename.fill(botany);
			await page.getByRole("button", { name: "Save" }).click();
			await expect(card(page, botany)).toBeVisible();
			await expect
				.poll(async () => (await listNotebooks(request, token)).map((r) => r.title))
				.toContain(botany);

			// Pin to featured → a Featured section appears.
			await card(page, botany).getByRole("button", { name: "More actions" }).click();
			await page.getByRole("menuitem", { name: "Pin to featured" }).click();
			await expect(page.getByRole("heading", { name: "Featured notebooks" })).toBeVisible();
			await expectNoOverflow(page, "home with featured row");
			await card(page, botany).getByRole("button", { name: "More actions" }).click();
			await page.getByRole("menuitem", { name: "Remove from featured" }).click();
			await expect(page.getByRole("heading", { name: "Featured notebooks" })).toBeHidden();

			// Copy → navigates to the copy.
			await card(page, beta).getByRole("button", { name: "More actions" }).click();
			await page.getByRole("menuitem", { name: "Copy notebook" }).click();
			await expect(page).toHaveURL(/\/notebooks\/\d+/);
			await expect(page.getByRole("textbox", { name: "Notebook title" }).first()).toHaveValue(
				copied
			);

			// Delete with the inline confirm.
			await page.goto(home());
			await card(page, copied).getByRole("button", { name: "More actions" }).click();
			await page.getByRole("menuitem", { name: "Delete" }).click();
			const confirm = page.getByRole("alert").filter({ hasText: `Delete “${copied}”?` });
			await expect(confirm).toBeVisible();
			await expectNoOverflow(page, "inline delete confirm");
			await confirm.getByRole("button", { name: "Delete" }).click();
			await expect
				.poll(async () => (await listNotebooks(request, token)).map((r) => r.title))
				.not.toContain(copied);
			await expect(card(page, copied)).toBeHidden();
		} finally {
			await notebooks.cleanup();
			// The copy was made by the page; remove it too if the test stopped early.
			for (const row of await listNotebooks(request, token).catch(() => [])) {
				if (row.title.includes(tag)) await deleteNotebook(request, token, row.id).catch(() => {});
			}
		}
	});

	test("search looks inside sources and notes, and opens what it found", async ({
		page,
		request,
		token,
		isPhone,
	}) => {
		// A made-up word, new each run, so only this test's rows can match (the
		// account is shared with the other project running the same test).
		const word = `quokka${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
		const lungsTitle = uniqueName("Lung clinic");
		const pharmacyTitle = uniqueName("Pharmacy");
		const unrelatedTitle = uniqueName("Unrelated");
		const notebooks = made(request, token);
		try {
			const lungs = notebooks.track(await createNotebook(request, token, lungsTitle, "🩺"));
			await addNoteSource(
				request,
				token,
				lungs.id,
				"Spacer technique",
				`Children should use a ${word} with every inhaled corticosteroid dose. Rinse the mouth afterwards.`
			);
			const pharmacy = notebooks.track(await createNotebook(request, token, pharmacyTitle, "💊"));
			const dosing = await createNote(
				request,
				token,
				pharmacy.id,
				"Dosing chart",
				`Stock one ${word} per ward and check the valve weekly.`
			);
			// A note is searched once the indexer has read it (after a short
			// delay, longer on a busy machine); the box searches as it is typed
			// and does not search again, so wait for that first.
			await expect
				.poll(async () => (await getNote(request, token, dosing.id)).index_state, {
					timeout: 60_000,
				})
				.toBe("ready");
			notebooks.track(await createNotebook(request, token, unrelatedTitle, "📓"));

			await page.goto(home());
			await expect(card(page, unrelatedTitle)).toBeVisible();
			const search = page.getByRole("textbox", { name: "Search notebooks" });

			// One character: titles filter, nothing is searched inside yet.
			await search.fill("q");
			await expect(page.getByRole("heading", { name: "Found inside notebooks" })).toBeHidden();

			await search.fill(word);
			// No title matches, but the content search finds both notebooks.
			await expect(page.getByText(/No notebooks match/)).toBeVisible();
			const found = page.locator("section[aria-labelledby='content-search-title']");
			await expect(found.getByRole("heading", { name: "Found inside notebooks" })).toBeVisible();
			await expect(found.getByRole("article", { name: lungsTitle })).toBeVisible();
			await expect(found.getByRole("article", { name: pharmacyTitle })).toBeVisible();
			await expect(found.getByRole("article", { name: unrelatedTitle })).toHaveCount(0);
			await expect(found.locator("mark", { hasText: word }).first()).toBeVisible();
			const sourceHit = found
				.getByRole("article", { name: lungsTitle })
				.getByRole("link", { name: /Spacer technique/ })
				.first();
			await expect(sourceHit).toHaveAttribute("href", /\?(source|note)=\d+$/);
			const noteHit = found
				.getByRole("article", { name: pharmacyTitle })
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
			await expect(found.getByRole("article", { name: pharmacyTitle })).toBeVisible();

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
			await page.goto(home());
			await page.getByRole("textbox", { name: "Search notebooks" }).fill(word);
			const docHit = page
				.locator("section[aria-labelledby='content-search-title']")
				.getByRole("article", { name: lungsTitle })
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
		} finally {
			await notebooks.cleanup();
		}
	});
});
