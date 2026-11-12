import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, test } from "./fixtures";

/** The dashboard redirect, the app shell's navigation, sign-in and sign-out. */

test.describe("Redirects", () => {
	// The dashboard's own address opens the notebooks home. Addresses from
	// before workspaces were removed (/dashboard/<id>/...) are no longer
	// forwarded: the owner retired them, so they are not listed here.
	test("/dashboard → /dashboard/notebooks", async ({ page }) => {
		await page.goto("/dashboard");
		await expect(page).toHaveURL(/\/dashboard\/notebooks$/);
	});
});

test.describe("App shell", () => {
	test("navigation: home, notes and the account menu's Settings", async ({ page, isPhone }) => {
		await page.goto("/dashboard/notebooks");
		await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible();
		const sheet = page.getByRole("dialog", { name: "Navigation" });
		const scope = isPhone ? sheet : page;
		if (isPhone) {
			await page.getByRole("button", { name: "Open menu" }).click();
			await expect(sheet).toBeVisible();
			await expectNoOverflow(page, "phone navigation sheet");
		}
		// Home and Notes, and nothing of a workspace: no switcher, no "Add workspace".
		await expect(scope.getByRole("button", { name: "Home" }).first()).toBeVisible();
		await expect(scope.getByRole("button", { name: "Notes" }).first()).toBeVisible();
		await expect(scope.getByRole("button", { name: /workspace/i })).toHaveCount(0);

		// The avatar button is named by the user's display name or email prefix.
		await scope
			.getByRole("button", { name: /e2e-test|E2E/i })
			.last()
			.click();
		await expectNoOverflow(page, "account menu");
		for (const gone of ["Manage members", "Workspace settings", "Switch workspace"]) {
			await expect(page.getByRole("menuitem", { name: gone })).toHaveCount(0);
		}
		await page.getByRole("menuitem", { name: "Settings" }).click();
		await expect(page).toHaveURL(/\/dashboard\/settings\/profile$/);
	});
});

test.describe("Auth", () => {
	test.use({ storageState: { cookies: [], origins: [] } });

	test("signed out: the dashboard sends you to /login, which fits the screen", async ({ page }) => {
		await page.goto("/dashboard");
		await expect(page).toHaveURL(/\/login/);
		await expect(page.getByRole("heading", { name: "Sign In" })).toBeVisible();
		await expectNoOverflow(page, "login page");
	});

	test("sign in with email and password, then log out", async ({ page, isPhone }) => {
		await page.goto("/login");
		await page.getByLabel("Email").fill(process.env.PLAYWRIGHT_TEST_EMAIL ?? "");
		await page
			.getByLabel("Password", { exact: true })
			.fill(process.env.PLAYWRIGHT_TEST_PASSWORD ?? "");
		await page.getByRole("button", { name: "Sign In" }).click();
		await expect(page).toHaveURL(/\/dashboard\/notebooks/, { timeout: 30_000 });

		if (isPhone) {
			await page.getByRole("button", { name: "Open menu" }).click();
		}
		const scope = isPhone ? page.getByRole("dialog", { name: "Navigation" }) : page;
		// The avatar button is named by the user's display name or email prefix.
		await scope
			.getByRole("button", { name: /e2e-test|E2E/i })
			.last()
			.click();
		await expectNoOverflow(page, "user menu");
		await page.getByRole("menuitem", { name: "Logout" }).click();
		await expect(page).toHaveURL(/\/login/);
		// The session is really gone: the dashboard bounces back to /login.
		await page.goto("/dashboard");
		await expect(page).toHaveURL(/\/login/);
	});

	test("wrong password shows an inline error", async ({ page }) => {
		await page.goto("/login");
		await page.getByLabel("Email").fill(process.env.PLAYWRIGHT_TEST_EMAIL ?? "");
		await page.getByLabel("Password", { exact: true }).fill("not-the-password");
		await page.getByRole("button", { name: "Sign In" }).click();
		await expect(page.getByRole("button", { name: "Dismiss error" })).toBeVisible();
		await expect(page).toHaveURL(/\/login/);
	});
});
