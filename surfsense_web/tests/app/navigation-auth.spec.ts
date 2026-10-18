import { expectNoOverflow } from "../helpers/layout/overflow";
import { waitForHumanCheck } from "../helpers/ui/human-check";
import { expect, test } from "./fixtures";

/** Legacy redirects, the app shell's navigation, sign-in and sign-out. */

test.describe("Redirects", () => {
	for (const [from, to] of [
		["", "/notebooks"],
		["/chats", "/notebooks"],
		["/new-chat", "/notebooks"],
		["/connectors", "/workspace-settings/connectors"],
		["/automations", "/workspace-settings/automations"],
		["/user-settings", "/user-settings/profile"],
		["/workspace-settings", "/workspace-settings/general"],
	] as const) {
		test(`/dashboard/<ws>${from || "/"} → ${to}`, async ({ page, workspace }) => {
			await page.goto(`/dashboard/${workspace.id}${from}`);
			await expect(page).toHaveURL(new RegExp(`/dashboard/${workspace.id}${to}$`));
		});
	}

	test("an unknown workspace falls back to one of yours", async ({ page }) => {
		await page.goto("/dashboard/999999/notebooks");
		await expect(page).toHaveURL(/\/dashboard\/(?!999999)\d+\/notebooks$/);
	});

	test("a legacy link to a chat that does not exist lands on the home, inline", async ({
		page,
		workspace,
	}) => {
		await page.goto(`/dashboard/${workspace.id}/new-chat/987654`);
		await expect(page).toHaveURL(new RegExp(`/dashboard/${workspace.id}/notebooks$`));
	});
});

test.describe("App shell", () => {
	test("navigation: home, workspace menu, settings", async ({ page, workspace, isPhone }) => {
		await page.goto(`/dashboard/${workspace.id}/notebooks`);
		await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible();
		if (isPhone) {
			await page.getByRole("button", { name: "Open menu" }).click();
			const sheet = page.getByRole("dialog", { name: "Navigation" });
			await expect(sheet).toBeVisible();
			await expectNoOverflow(page, "phone navigation sheet");
			await sheet.getByRole("button", { name: workspace.name }).first().click();
		} else {
			await page.getByRole("button", { name: workspace.name }).first().click();
		}
		await expect(page.getByRole("menuitem", { name: "Manage members" })).toBeVisible();
		await expectNoOverflow(page, "workspace menu");
		await page.getByRole("menuitem", { name: "Workspace settings" }).click();
		await expect(page).toHaveURL(/\/workspace-settings\/general$/);
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
		await waitForHumanCheck(page);
		await page.getByRole("button", { name: "Sign In" }).click();
		await expect(page).toHaveURL(/\/dashboard\/\d+\/notebooks/, { timeout: 30_000 });

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
		await waitForHumanCheck(page);
		await page.getByRole("button", { name: "Sign In" }).click();
		await expect(page.getByRole("button", { name: "Dismiss error" })).toBeVisible();
		await expect(page).toHaveURL(/\/login/);
	});
});
