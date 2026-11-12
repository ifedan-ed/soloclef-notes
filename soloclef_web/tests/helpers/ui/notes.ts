import { expect, type Page } from "@playwright/test";

/**
 * A notebook's Notes tab, opened: the notebook page, then on a phone its
 * "Studio & notes" tab (the right-hand column is a tab there; on a desktop
 * it is already shown), then the column's own Notes tab.
 */
export async function openNotesTab(page: Page, notebookId: number): Promise<void> {
	await page.goto(`/dashboard/notebooks/${notebookId}`);
	await expect(page.getByRole("textbox", { name: "Notebook title" }).first()).toBeVisible();
	if ((page.viewportSize()?.width ?? 1280) < 1024) {
		await page.getByRole("tab", { name: "Studio & notes" }).click();
	}
	const tab = page.getByRole("tab", { name: /^Notes/ });
	await tab.click();
	await expect(tab).toHaveAttribute("aria-selected", "true");
}

/** The open note: its view is the article that holds the title field. */
export const openNote = (page: Page) =>
	page.locator("article").filter({ has: page.getByRole("textbox", { name: "Note title" }) });

/**
 * The note's editor, found by its text: a notebook's page has the chat's
 * box too, also an editor.
 */
export const noteEditor = (page: Page, text: string) =>
	page.locator("[data-slate-editor]", { hasText: text });

/** The create's answer, for the id of a note the page made with New note. */
export const noteCreated = (page: Page) =>
	page.waitForResponse(
		(r) => r.request().method() === "POST" && /\/api\/v1\/notes$/.test(new URL(r.url()).pathname)
	);
