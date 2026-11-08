import type { APIRequestContext, Browser, Page } from "@playwright/test";
import { BACKEND_URL, mintTestToken } from "../helpers/api/auth";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, test } from "./fixtures";

/**
 * Invite by email, end to end on the isolated stack: the Owner invites a
 * registered account, the invitation email lands in the E2E outbox, another
 * account cannot accept it, the invitee can, and the invite leaves the list.
 */

const MINT_SECRET = process.env.E2E_MINT_SECRET ?? "";
const SESSION_COOKIE_NAME = process.env.SESSION_COOKIE_NAME || "surfsense_session";

/** A registered, verified account, created as an administrator would (sign-ups are closed). */
async function registerFreshUser(request: APIRequestContext, label: string): Promise<string> {
	const email = `${label}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}@example.com`;
	const response = await request.post(`${BACKEND_URL}/__e2e__/accounts`, {
		data: { email },
		headers: { "Content-Type": "application/json", "X-E2E-Mint-Secret": MINT_SECRET },
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

/** A separate browser signed in as ``email``. */
async function pageSignedInAs(
	browser: Browser,
	request: APIRequestContext,
	baseURL: string,
	email: string
): Promise<Page> {
	const token = await mintTestToken(request, email);
	const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
	await context.addCookies([
		{ name: SESSION_COOKIE_NAME, value: token, url: baseURL, httpOnly: true, sameSite: "Lax" },
	]);
	return context.newPage();
}

async function openInviteDialog(page: Page) {
	await page.getByRole("button", { name: "Invite", exact: true }).click();
	const dialog = page.getByRole("dialog", { name: "Invite someone" });
	await expect(dialog).toBeVisible();
	return dialog;
}

test("an unregistered address is refused inline: sign-ups are closed", async ({
	page,
	workspace,
}) => {
	await page.goto(`/dashboard/${workspace.id}/team`);
	const dialog = await openInviteDialog(page);
	await dialog.getByLabel("Email address").fill(`nobody-${Date.now().toString(36)}@example.com`);
	await dialog.getByRole("button", { name: "Send invitation" }).click();
	await expect(dialog.getByRole("alert")).toContainText("Sign-ups are closed");
	await expectNoOverflow(page, "invite dialog with error");
});

test("invite by email: sent, refused for another account, accepted by the invitee", async ({
	page,
	workspace,
	browser,
	baseURL,
	playwright,
}) => {
	test.setTimeout(120_000);
	const base = baseURL ?? "http://127.0.0.1:3940";
	// Without the page's session cookie: a cookie-carrying POST from outside
	// the page would (rightly) fail the CSRF origin check.
	const request = await playwright.request.newContext({
		storageState: { cookies: [], origins: [] },
	});
	const invitee = await registerFreshUser(request, "invitee");
	const stranger = await registerFreshUser(request, "stranger");

	await page.goto(`/dashboard/${workspace.id}/team`);
	await expect(page.getByRole("heading", { name: "Members", level: 1 })).toBeVisible();
	await expect(page.getByRole("heading", { name: "Pending invitations" })).toBeVisible();
	await expectNoOverflow(page, "team page");

	const dialog = await openInviteDialog(page);
	await expectNoOverflow(page, "invite dialog");
	await dialog.getByLabel("Email address").fill(invitee);
	await dialog.getByRole("button", { name: "Send invitation" }).click();
	await expect(page.getByRole("dialog", { name: "Invitation sent" })).toBeVisible();
	await page.getByRole("button", { name: "Done" }).click();

	const pending = page.getByTestId("pending-invite").filter({ hasText: invitee });
	await expect(pending).toBeVisible();

	const mail = await lastMailTo(request, invitee);
	expect(mail.subject).toContain(workspace.name);
	const link = mail.text.match(/https?:\/\/\S+\/invite\/\S+/)?.[0];
	expect(link, mail.text).toBeTruthy();
	const invitePath = new URL(link as string).pathname;

	// Another account opens the link: refused, with a way to switch.
	const other = await pageSignedInAs(browser, request, base, stranger);
	await other.goto(invitePath);
	await expect(other.getByText(/invited you to/)).toBeVisible();
	await expect(other.getByText(/This invitation is for /)).toBeVisible();
	await other.getByRole("button", { name: "Accept invitation" }).click();
	await expect(other.getByRole("alert").filter({ hasText: "another account" })).toBeVisible();
	await expect(other.getByRole("button", { name: "Sign out and switch account" })).toBeVisible();
	await expectNoOverflow(other, "invite page, wrong account");
	await other.context().close();

	// The invitee accepts and lands in the workspace.
	const joiner = await pageSignedInAs(browser, request, base, invitee);
	await joiner.goto(invitePath);
	await joiner.getByRole("button", { name: "Accept invitation" }).click();
	await expect(joiner.getByRole("heading", { name: "You're in" })).toBeVisible();
	await joiner.getByRole("button", { name: "Open the workspace" }).click();
	await expect(joiner).toHaveURL(new RegExp(`/dashboard/${workspace.id}/notebooks`));
	await joiner.context().close();

	// Back on the Team page: a member now, no longer pending.
	await page.reload();
	await expect(page.getByTestId("member-row").filter({ hasText: invitee })).toBeVisible();
	await expect(page.getByTestId("pending-invite").filter({ hasText: invitee })).toHaveCount(0);
	await request.dispose();
});

test("an invite link: any signed-in account can join with it", async ({
	page,
	workspace,
	browser,
	baseURL,
	playwright,
}) => {
	test.setTimeout(120_000);
	const base = baseURL ?? "http://127.0.0.1:3940";
	const request = await playwright.request.newContext({
		storageState: { cookies: [], origins: [] },
	});
	const joinerEmail = await registerFreshUser(request, "linkjoiner");

	await page.goto(`/dashboard/${workspace.id}/team`);
	const dialog = await openInviteDialog(page);
	await dialog.getByRole("button", { name: "Invite link" }).click();
	await expect(dialog.getByLabel("Email address")).toBeHidden();
	await dialog.getByLabel("Maximum uses (optional)").fill("3");
	await dialog.getByRole("button", { name: "Create link" }).click();
	const created = page.getByRole("dialog", { name: "Invite link created" });
	await expect(created).toBeVisible();
	const link = (await created.locator("code").innerText()).trim();
	await page.getByRole("button", { name: "Done" }).click();
	await expect(page.getByTestId("pending-invite").filter({ hasText: "0 of 3 used" })).toBeVisible();

	const joiner = await pageSignedInAs(browser, request, base, joinerEmail);
	await joiner.goto(new URL(link).pathname);
	await expect(joiner.getByText(/This invitation is for /)).toHaveCount(0);
	await joiner.getByRole("button", { name: "Accept invitation" }).click();
	await expect(joiner.getByRole("heading", { name: "You're in" })).toBeVisible();
	await joiner.context().close();

	await page.reload();
	await expect(page.getByTestId("member-row").filter({ hasText: joinerEmail })).toBeVisible();
	await expect(page.getByTestId("pending-invite").filter({ hasText: "1 of 3 used" })).toBeVisible();
	await request.dispose();
});
