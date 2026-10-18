import type { APIRequestContext, Page } from "@playwright/test";
import { BACKEND_URL } from "../helpers/api/auth";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { waitForHumanCheck } from "../helpers/ui/human-check";
import { expect, test } from "./fixtures";

/**
 * Forgot password -> email -> new password, end to end on the isolated stack.
 * The reset email lands in the E2E backend's outbox (no SMTP there); the
 * link in it is followed exactly as a person would.
 */

const MINT_SECRET = process.env.E2E_MINT_SECRET ?? "";
const OLD_PASSWORD = "Old e2e password 1";
const NEW_PASSWORD = "New e2e password 2";

test.use({ storageState: { cookies: [], origins: [] } });

async function registerFreshUser(request: APIRequestContext): Promise<string> {
	const email = `reset-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}@example.com`;
	const response = await request.post(`${BACKEND_URL}/auth/register`, {
		data: { email, password: OLD_PASSWORD },
		// The stack's Turnstile secret is Cloudflare's always-pass test key.
		headers: { "Content-Type": "application/json", "X-Turnstile-Token": "XXXX.DUMMY.TOKEN.XXXX" },
	});
	expect(response.status(), await response.text()).toBe(201);
	return email;
}

async function lastMailTo(request: APIRequestContext, email: string) {
	const response = await request.get(
		`${BACKEND_URL}/__e2e__/mail/last?to=${encodeURIComponent(email)}`,
		{ headers: { "X-E2E-Mint-Secret": MINT_SECRET } }
	);
	expect(response.ok(), await response.text()).toBeTruthy();
	return (await response.json()) as { subject: string; text: string };
}

async function signIn(page: Page, email: string, password: string) {
	await page.goto("/login");
	await page.getByLabel("Email").fill(email);
	await page.getByLabel("Password", { exact: true }).fill(password);
	await waitForHumanCheck(page);
	await page.getByRole("button", { name: "Sign In" }).click();
}

test("the sign-in page links to Forgot password, and that page fits", async ({ page }) => {
	await page.goto("/login");
	await expect(page.getByTestId("human-check")).toBeVisible();
	await page.getByRole("link", { name: "Forgot Password?" }).click();
	await expect(page).toHaveURL(/\/forgot-password$/);
	await expect(page.getByRole("heading", { name: "Reset your password" })).toBeVisible();
	await waitForHumanCheck(page);
	await expectNoOverflow(page, "forgot password page");
});

test("a new password that does not match is caught before anything is sent", async ({ page }) => {
	await page.goto("/reset-password#token=not-a-real-token");
	await page.getByLabel("New password", { exact: true }).fill("long enough 1");
	await page.getByLabel("Confirm new password").fill("long enough 2");
	await page.getByRole("button", { name: "Set new password" }).click();
	await expect(page.getByRole("alert").filter({ hasText: "do not match" })).toBeVisible();
	await expectNoOverflow(page, "reset password page");
});

test("forgot password: email, link, new password, sign in; the link then stops working", async ({
	page,
	request,
	isPhone,
}) => {
	// One full run per suite: the reset routes are rate limited per IP.
	test.skip(isPhone, "covered once, on desktop");
	const email = await registerFreshUser(request);

	await page.goto("/forgot-password");
	await page.getByLabel("Email").fill(email);
	await waitForHumanCheck(page);
	await page.getByRole("button", { name: "Send reset link" }).click();
	await expect(page.getByRole("status").filter({ hasText: "Check your email" })).toBeVisible();
	await expect(page.getByRole("status").filter({ hasText: email })).toBeVisible();

	const mail = await lastMailTo(request, email);
	expect(mail.subject).toBe("Reset your Soloclef Notes password");
	const link = mail.text.match(/https?:\/\/\S+\/reset-password#token=\S+/)?.[0];
	expect(link, mail.text).toBeTruthy();
	const path = new URL(link as string);
	await page.goto(`${path.pathname}${path.hash}`);

	await page.getByLabel("New password", { exact: true }).fill(NEW_PASSWORD);
	await page.getByLabel("Confirm new password").fill(NEW_PASSWORD);
	await waitForHumanCheck(page);
	await page.getByRole("button", { name: "Set new password" }).click();
	await expect(page.getByRole("status").filter({ hasText: "Password changed" })).toBeVisible();
	// The spent token is taken out of the address bar.
	expect(new URL(page.url()).hash).toBe("");

	await signIn(page, email, NEW_PASSWORD);
	await expect(page).toHaveURL(/\/dashboard\/\d+\/notebooks/, { timeout: 30_000 });

	// Same link again: refused, with a way to ask for a new one.
	await page.context().clearCookies();
	await page.goto(`${path.pathname}${path.hash}`);
	await page.getByLabel("New password", { exact: true }).fill("Third password 3");
	await page.getByLabel("Confirm new password").fill("Third password 3");
	await waitForHumanCheck(page);
	await page.getByRole("button", { name: "Set new password" }).click();
	await expect(
		page.getByRole("alert").filter({ hasText: "expired or was already used" })
	).toBeVisible();
	await expect(page.getByRole("link", { name: "Send a new link" })).toBeVisible();
});

test("an address with no account gets the same answer", async ({ page, isPhone }) => {
	test.skip(isPhone, "covered once, on desktop");
	const email = `nobody-${Date.now().toString(36)}@example.com`;
	await page.goto("/forgot-password");
	await page.getByLabel("Email").fill(email);
	await waitForHumanCheck(page);
	await page.getByRole("button", { name: "Send reset link" }).click();
	await expect(page.getByRole("status").filter({ hasText: "Check your email" })).toBeVisible();
	await expect(page.getByRole("status").filter({ hasText: email })).toBeVisible();
});
