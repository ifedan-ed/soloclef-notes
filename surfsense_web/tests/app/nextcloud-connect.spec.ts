import type { Page } from "@playwright/test";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, openNotebook, test } from "./fixtures";

/**
 * Connecting Nextcloud in one click, against the stand-in Nextcloud of the
 * e2e stack (tests/e2e/fakes/nextcloud_server.py, reached at /__nextcloud__).
 * The sign-in opens in a new tab; granting access there connects this page.
 */

const SERVER = "nextcloud-stub:8080";

async function signInInNewTab(page: Page, scope: ReturnType<Page["locator"]>) {
	const link = scope.getByRole("link", { name: `Sign in to ${SERVER}` });
	await expect(link).toBeVisible();
	await expect(link).toHaveAttribute("target", "_blank");
	const [nextcloud] = await Promise.all([page.context().waitForEvent("page"), link.click()]);
	await nextcloud.getByRole("button", { name: "Grant access" }).click();
	await expect(nextcloud.getByText("Account connected")).toBeVisible();
	await nextcloud.close();
}

async function revokedCount(page: Page): Promise<number> {
	const response = await page.request.get("/__nextcloud__/__stub__/state");
	return ((await response.json()) as { revoked: number }).revoked;
}

test.describe("Nextcloud connect", () => {
	test("the connectors catalog connects Nextcloud next to Google Drive", async ({
		page,
		workspace,
	}) => {
		await page.goto(`/dashboard/${workspace.id}/workspace-settings/connectors`);
		const card = page.getByTestId("nextcloud-connector-card");
		await expect(card).toContainText(`Connect your Nextcloud (${SERVER})`);
		await expectNoOverflow(page, "connectors catalog with Nextcloud");

		// Right after Google Drive in the grid.
		const titles = await page
			.locator("span.font-semibold")
			.filter({ hasText: /^(Google Drive|Nextcloud)$/ })
			.allTextContents();
		expect(titles.slice(0, 2)).toEqual(["Google Drive", "Nextcloud"]);

		await card.getByRole("button", { name: "Connect", exact: true }).click();
		await signInInNewTab(page, card);
		await expect(card).toContainText("Connected as E2E Nextcloud User");
		await expect(card.getByRole("button", { name: "Manage" })).toBeVisible();

		await card.getByRole("button", { name: "Manage" }).click();
		await expect(page.getByTestId("nextcloud-account")).toHaveText(
			"Connected as E2E Nextcloud User"
		);
		await expect(page.getByRole("button", { name: "Reconnect" })).toBeVisible();

		const before = await revokedCount(page);
		await page.getByRole("button", { name: "Disconnect", exact: true }).click();
		await page.getByRole("button", { name: "Confirm Disconnect" }).click();
		await expect.poll(() => revokedCount(page)).toBeGreaterThan(before);
	});

	test("a started sign-in can be cancelled", async ({ page, workspace }) => {
		await page.goto(`/dashboard/${workspace.id}/workspace-settings/connectors`);
		const card = page.getByTestId("nextcloud-connector-card");
		await card.getByRole("button", { name: "Connect", exact: true }).click();
		await expect(card.getByRole("link", { name: `Sign in to ${SERVER}` })).toBeVisible();
		await card.getByRole("button", { name: "Cancel" }).click();
		await expect(card.getByRole("link", { name: `Sign in to ${SERVER}` })).toHaveCount(0);
		await expect(card.getByRole("button", { name: "Connect", exact: true })).toBeEnabled();
	});

	test("Add sources connects in place, then shows the picker; Disconnect revokes", async ({
		page,
		workspace,
		notebook,
	}) => {
		await openNotebook(page, workspace.id, notebook.id, "Sources");
		await page.getByRole("button", { name: "Add sources" }).first().click();
		const dialog = page.getByRole("dialog", { name: "Add sources" });
		await dialog.getByRole("tab", { name: /connector/i }).click();
		// Google Drive sits above Nextcloud here; each has its own Connect.
		const nextcloud = dialog.getByTestId("nextcloud-sources");
		await expect(nextcloud).toContainText(`Connect your Nextcloud (${SERVER})`);
		// No field anywhere asks for a server, user or password.
		await expect(dialog.getByRole("textbox", { name: /password|server|address/i })).toHaveCount(0);

		await nextcloud.getByRole("button", { name: "Connect", exact: true }).click();
		await signInInNewTab(page, nextcloud);
		await expect(dialog.getByTestId("nextcloud-account")).toHaveText(
			"Connected as E2E Nextcloud User"
		);
		await expect(dialog.getByText("Welcome.md")).toBeVisible();
		await expectNoOverflow(page, "Add sources: Nextcloud connected");

		const before = await revokedCount(page);
		await nextcloud.getByRole("button", { name: "Disconnect", exact: true }).click();
		await nextcloud.getByRole("button", { name: "Disconnect", exact: true }).last().click();
		await expect(nextcloud.getByRole("button", { name: "Connect", exact: true })).toBeVisible();
		await expect(dialog.getByText("Welcome.md")).toHaveCount(0);
		await expect.poll(() => revokedCount(page)).toBeGreaterThan(before);
	});
});
