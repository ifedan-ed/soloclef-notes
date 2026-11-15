import type { APIRequestContext, Page } from "@playwright/test";
import { BACKEND_URL } from "../helpers/api/auth";
import { seedOutput } from "../helpers/api/notebooks";
import { OWN_ACCOUNT_PASSWORD, signInAsNewAccount } from "../helpers/api/own-account";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, openNotebook, test, uniqueName } from "./fixtures";

/**
 * Settings, Security (features/account-security), and the smaller fixes that
 * came with it: Forgot password on a server that sends no mail, and Share on
 * an audio overview.
 *
 * Every test that changes the account (its password, its sign-ins) makes an
 * account of its own: the shared e2e user's sign-ins hold up every other
 * spec. Tests that only look use the shared user. The single sign-on branch cannot run for real here (the stack has no
 * identity provider), so its answers are the API's own, served by a route
 * stub, and the provider's redirect is caught where the browser leaves.
 */

const SECURITY = "/dashboard/settings/security";

/** Sign the account in as a phone, by the route the phone app uses; its session id is answered. */
async function signInPhone(request: APIRequestContext, email: string, label: string) {
	const response = await request.post(`${BACKEND_URL}/auth/desktop/login`, {
		data: { email, password: OWN_ACCOUNT_PASSWORD, device_label: label },
		headers: { "Content-Type": "application/json", Origin: new URL(BACKEND_URL).origin },
	});
	expect(response.ok(), await response.text()).toBeTruthy();
	return (await response.json()) as { session_id: string; refresh_token: string };
}

/** Whether a phone's refresh token still works: what "signed out" means for it. */
async function phoneStillSignedIn(request: APIRequestContext, refreshToken: string) {
	const response = await request.post(`${BACKEND_URL}/auth/jwt/refresh`, {
		data: { refresh_token: refreshToken },
		headers: { "Content-Type": "application/json", Origin: new URL(BACKEND_URL).origin },
	});
	return response.ok();
}

async function openSecurity(page: Page) {
	await page.goto(SECURITY);
	await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible();
	await expect(page.getByTestId("password-card")).toBeVisible();
}

test.describe("Settings, Security", () => {
	// Opening the page changes nothing, so the shared e2e user serves.
	test("the section lists this browser first and fits", async ({ page }) => {
		await openSecurity(page);
		await expect(page.getByRole("heading", { name: "Password", exact: true })).toBeVisible();
		await expect(page.getByLabel("Current password")).toBeVisible();
		const rows = page.getByTestId("device-row");
		await expect(rows.first()).toContainText("This browser");
		await expect(rows.first()).toHaveAttribute("data-current", "true");
		await expect(rows.filter({ hasText: "This browser" })).toHaveCount(1);
		await expect(page.getByRole("button", { name: "Sign out everywhere" })).toBeVisible();
		await expectNoOverflow(page, "settings: security");
	});

	// The tests below make accounts and sign in with passwords. Making an
	// account, a password sign-in and a password change all count against
	// the sign-in routes' one limit per client (10 a minute), so each runs
	// once, on desktop, and does as much as it can with one account.

	test("change the password: refusals first, then the phone is signed out and this browser stays", async ({
		page,
		request,
		isPhone,
	}) => {
		test.skip(isPhone, "covered once, on desktop: the sign-in limit");
		const { email } = await signInAsNewAccount(page, request, "security-change");
		const phone = await signInPhone(request, email, "E2E Pixel");
		await openSecurity(page);
		const problem = page.getByTestId("password-problem");

		// Caught here, before anything is sent.
		await page.getByLabel("Current password").fill(OWN_ACCOUNT_PASSWORD);
		await page.getByLabel("New password", { exact: true }).fill("A brand new password 9");
		await page.getByLabel("New password again").fill("A brand new password 8");
		await page.getByRole("button", { name: "Change password" }).click();
		await expect(problem).toHaveText("The two new passwords are different.");

		// Refused by the server.
		await page.getByLabel("Current password").fill("not the password");
		await page.getByLabel("New password again").fill("A brand new password 9");
		await page.getByRole("button", { name: "Change password" }).click();
		await expect(problem).toHaveText("That isn't your current password.");

		await page.getByLabel("Current password").fill(OWN_ACCOUNT_PASSWORD);
		await page.getByRole("button", { name: "Change password" }).click();
		await expect(page.getByText("Password changed. Signed out of 1 other device.")).toBeVisible();
		await expect(problem).toHaveCount(0);
		expect(await phoneStillSignedIn(request, phone.refresh_token)).toBe(false);
		// This browser is still signed in, and the list is down to it.
		await expect(page.getByTestId("device-row")).toHaveCount(1);
		await expect(page.getByTestId("device-row")).toContainText("This browser");
		await expect(page.getByLabel("Current password")).toHaveValue("");
	});

	test("sign one phone out from the list, then sign out everywhere", async ({
		page,
		request,
		isPhone,
	}) => {
		test.skip(isPhone, "covered once, on desktop: the sign-in limit");
		const { email } = await signInAsNewAccount(page, request, "security-devices");
		const label = uniqueName("E2E iPhone");
		const phone = await signInPhone(request, email, label);
		const tablet = await signInPhone(request, email, "E2E tablet");
		await openSecurity(page);

		const row = page.getByTestId("device-row").filter({ hasText: label });
		await expect(row).toContainText(/Signed in .* · last used .* · ends /);
		await row.getByRole("button", { name: `Sign out ${label}` }).click();
		const dialog = page.getByRole("alertdialog");
		await expect(dialog).toContainText(`${label} will have to sign in again.`);
		await expectNoOverflow(page, "security: sign a device out");
		await dialog.getByRole("button", { name: "Sign out device" }).click();
		await expect(row).toHaveCount(0);
		expect(await phoneStillSignedIn(request, phone.refresh_token)).toBe(false);
		await expect(page.getByTestId("device-row").first()).toContainText("This browser");

		await page.getByRole("button", { name: "Sign out everywhere" }).click();
		await expect(dialog).toContainText("this one included");
		await dialog.getByRole("button", { name: "Sign out everywhere" }).click();
		await expect(page).toHaveURL(/\/login/, { timeout: 30_000 });
		expect(await phoneStillSignedIn(request, tablet.refresh_token)).toBe(false);
		// Back to Settings: the browser is signed out, so it is sent to sign in.
		await page.goto(SECURITY);
		await expect(page).toHaveURL(/\/login/, { timeout: 30_000 });
	});

	// Every answer that matters is stubbed and nothing is written, so the
	// shared e2e user serves.
	test("an account made by single sign-on confirms with the provider before a first password", async ({
		page,
	}) => {
		// The API's own answers for such an account, on a server with single sign-on.
		await page.route("**/auth/password", (route) =>
			route.fulfill({ json: { has_password: false } })
		);
		await page.route("**/api/v1/sign-in-options", (route) =>
			route.fulfill({
				json: {
					service: "soloclef",
					password: true,
					forgot_password: true,
					sign_up: true,
					sso: { label: "Continue with Example SSO", name: "Example SSO", app: false },
				},
			})
		);
		await page.route("**/auth/change-password", (route) =>
			route.fulfill({
				status: 403,
				json: {
					error: {
						code: "FORBIDDEN",
						message: "CHANGE_PASSWORD_CONFIRM_SSO",
						status: 403,
						request_id: "e2e",
					},
				},
			})
		);
		// Where the browser leaves for the provider: caught, not followed.
		let leftFor: URL | null = null;
		await page.route("**/auth/oidc/authorize-redirect**", (route) => {
			leftFor = new URL(route.request().url());
			return route.fulfill({ status: 204 });
		});

		await openSecurity(page);
		await expect(page.getByRole("heading", { name: "Set a password" })).toBeVisible();
		await expect(page.getByTestId("password-sso-note")).toContainText(
			"Your account was made through Example SSO"
		);
		await expect(page.getByLabel("Current password")).toHaveCount(0);
		await page.getByLabel("New password", { exact: true }).fill("A first password 1");
		await page.getByLabel("New password again").fill("A first password 1");
		await page.getByRole("button", { name: "Set password" }).click();

		const confirm = page.getByTestId("password-sso-confirm");
		await expect(confirm).toContainText("Confirm it's you with Example SSO");
		await expectNoOverflow(page, "security: confirm with single sign-on");
		await confirm.getByRole("button", { name: "Continue with Example SSO" }).click();
		await expect.poll(() => leftFor?.searchParams.get("next")).toBe(`${SECURITY}?confirmed=sso`);

		// Back from the provider: confirmed, and the marker is out of the address.
		await page.goto(`${SECURITY}?confirmed=sso`);
		await expect(page.getByTestId("password-sso-confirmed")).toContainText(
			"Confirmed with Example SSO. Set your password within 10 minutes."
		);
		await expect(page).toHaveURL(new RegExp(`${SECURITY}$`));
	});
});

test("Forgot password on a server that sends no mail says so plainly", async ({ browser }) => {
	const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
	const page = await context.newPage();
	await page.route("**/api/v1/sign-in-options", (route) =>
		route.fulfill({
			json: {
				service: "soloclef",
				password: true,
				forgot_password: false,
				sign_up: true,
				sso: null,
			},
		})
	);
	await page.goto("/forgot-password");
	await expect(page.getByTestId("reset-unavailable")).toContainText(
		"Password reset by email isn't available on this server"
	);
	await expect(page.getByRole("button", { name: "Send reset link" })).toHaveCount(0);
	await expect(page.getByRole("link", { name: /back to/i })).toBeVisible();
	await expectNoOverflow(page, "forgot password: no mail");
	await context.close();
});

test("an audio overview can be shared from its player", async ({ page, request, notebook }) => {
	const title = uniqueName("E2E shared audio");
	await seedOutput(request, notebook.id, "audio", "ready", title);
	await openNotebook(page, notebook.id, "Studio");
	await page
		.getByRole("button", { name: new RegExp(`^${title}`) })
		.first()
		.click();
	await expect(page.getByTestId("audio-overview-view")).toBeVisible();
	await page.getByRole("button", { name: "Share publicly" }).click();
	const dialog = page.getByTestId("share-dialog");
	await dialog.getByRole("button", { name: "Create link" }).click();
	const link = dialog.getByRole("textbox", { name: "Public link" });
	await expect(link).toHaveValue(/\/public\//);
	await expectNoOverflow(page, "audio overview: share");
	const url = await link.inputValue();
	await dialog.getByRole("button", { name: "Done" }).click();

	// A visitor gets the shared page's own player.
	const visitor = await page
		.context()
		.browser()
		?.newContext({ storageState: { cookies: [], origins: [] } });
	const visitorPage = await visitor?.newPage();
	if (!visitorPage) throw new Error("no browser for the visitor");
	await visitorPage.goto(url);
	await expect(visitorPage.getByTestId("public-output-audio")).toBeVisible({ timeout: 30_000 });
	await visitor?.close();
});
