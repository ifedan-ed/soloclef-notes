import { expect, type Page } from "@playwright/test";

/**
 * Wait until the Turnstile widget has handed the page a token. The isolated
 * stack uses Cloudflare's always-pass test site key, which solves itself in a
 * moment; the token lands in the widget's hidden response field.
 */
export async function waitForHumanCheck(page: Page): Promise<void> {
	await expect(page.locator('input[name="cf-turnstile-response"]').first()).toHaveValue(/.+/, {
		timeout: 30_000,
	});
}
