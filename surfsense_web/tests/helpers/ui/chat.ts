import type { Page } from "@playwright/test";

/** The notebook chat composer (a Plate contenteditable inside the composer root). */
export const composer = (page: Page) => page.locator('.aui-composer-root [contenteditable="true"]');

/** Type into the composer and press Send. */
export async function send(page: Page, text: string): Promise<void> {
	const box = composer(page);
	await box.click();
	await box.pressSequentially(text, { delay: 20 });
	await page.getByRole("button", { name: "Send message" }).click();
}

export const assistantMessages = (page: Page) => page.locator('[data-role="assistant"]');
export const userMessages = (page: Page) => page.locator('[data-role="user"]');
