import type { Page } from "@playwright/test";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, test } from "./fixtures";

/**
 * Billing tab: /dashboard/<ws>/user-settings/billing, and the /billing landing
 * Stripe, BTCPay and the apps return to. The e2e stack has no payment
 * provider configured, so the page shows the inline "not available yet" notice
 * and no buy buttons; the crypto-only case answers /billing/me as if BTCPay were set up.
 */

/** The plan, plans and packs: where payment methods would be named. */
function purchaseAreas(page: Page) {
	return page.locator(
		'[data-testid="billing-current-plan"], section[aria-labelledby="billing-plans-title"], section[aria-labelledby="billing-packs-title"]'
	);
}

async function openBilling(page: Page, ws: number) {
	await page.goto(`/dashboard/${ws}/user-settings/billing`);
	await expect(page.getByRole("heading", { name: "User Settings", level: 1 })).toBeVisible();
	await expect(page.getByTestId("billing-current-plan")).toBeVisible();
}

test.describe("Billing", () => {
	test("shows the plan, credits, plans and history, and fits", async ({ page, workspace }) => {
		await openBilling(page, workspace.id);
		await expect(page.getByText("Current plan", { exact: true }).first()).toBeVisible();
		await expect(page.getByTestId("billing-credits")).toBeVisible();
		await expect(page.getByTestId("billing-spendable")).toHaveText(/\d/);
		await expect(page.getByRole("heading", { name: "Plans", exact: true })).toBeVisible();
		await expect(page.getByRole("heading", { name: "Usage history" })).toBeVisible();
		await expect(page.getByRole("heading", { name: "Payments and invoices" })).toBeVisible();
		await expectNoOverflow(page, "billing tab");
	});

	test("with no payment provider, there is a clean notice and nothing to click", async ({
		page,
		workspace,
	}) => {
		await openBilling(page, workspace.id);
		await expect(page.getByTestId("billing-payments-unavailable").first()).toBeVisible();
		// No buttons that would lead nowhere: no Upgrade, Buy, portal or card copy.
		await expect(page.getByRole("button", { name: /^(Upgrade|Downgrade): / })).toHaveCount(0);
		await expect(page.getByRole("button", { name: /^Buy: / })).toHaveCount(0);
		await expect(page.getByRole("button", { name: "Manage subscription" })).toHaveCount(0);
		await expect(purchaseAreas(page).getByText(/\bcard\b|stripe/i)).toHaveCount(0);
		await expectNoOverflow(page, "billing tab without payments");
	});

	test("crypto only: plans are prepaid by crypto, and nothing mentions a card", async ({
		page,
		workspace,
	}) => {
		// The e2e stack has no BTCPay; the account is answered as if crypto were
		// set up and Stripe pending, which is how Soloclef Notes launches.
		await page.route(/\/api\/v1\/billing\/me$/, async (route) => {
			const response = await route.fetch();
			const body = await response.json();
			body.payments = {
				card: false,
				crypto: {
					enabled: true,
					methods: ["BTC-CHAIN", "USDT-TRON", "USDT-POLYGON"],
					month_options: [1, 3, 12],
					discounts: { "12": 10 },
				},
			};
			await route.fulfill({ response, json: body });
		});
		await openBilling(page, workspace.id);
		await expect(page.getByTestId("billing-payments-unavailable")).toHaveCount(0);
		await expect(page.getByText(/paid in crypto, 1, 3, or 12 months at a time/)).toBeVisible();
		await expect(page.getByRole("button", { name: "Manage subscription" })).toHaveCount(0);
		await expect(purchaseAreas(page).getByText(/\bcard\b|stripe/i)).toHaveCount(0);

		await page
			.getByRole("button", { name: /^Upgrade: / })
			.first()
			.click();
		const dialog = page.getByRole("dialog");
		await expect(dialog).toBeVisible();
		await expect(dialog.getByText(/per month, paid ahead/)).toBeVisible();
		await expect(dialog.getByText("Months", { exact: true })).toBeVisible();
		await expect(dialog.getByRole("button", { name: "Continue to crypto checkout" })).toBeVisible();
		await expect(dialog.getByRole("button", { name: "Continue to checkout" })).toHaveCount(0);
		await expect(dialog.getByText(/\bcard\b|stripe/i)).toHaveCount(0);
		await expectNoOverflow(page, "crypto-only upgrade dialog");
		await dialog.getByRole("button", { name: "Cancel" }).click();
		await expect(dialog).toBeHidden();

		const buy = page.getByRole("button", { name: /^Buy: / }).first();
		await expect(buy).toBeVisible();
		await buy.click();
		const packDialog = page.getByRole("dialog");
		await expect(
			packDialog.getByRole("button", { name: "Continue to crypto checkout" })
		).toBeVisible();
		await expect(packDialog.getByText(/\bcard\b|stripe/i)).toHaveCount(0);
		await packDialog.getByRole("button", { name: "Cancel" }).click();
		await expect(packDialog).toBeHidden();
	});

	test("the billing admin tab is hidden for a normal user", async ({ page, workspace }) => {
		await openBilling(page, workspace.id);
		await expect(page.getByRole("link", { name: "Billing admin" })).toHaveCount(0);
		await page.goto(`/dashboard/${workspace.id}/user-settings/billing-admin`);
		await expect(page.getByText("Only billing administrators can open this page.")).toBeVisible();
	});

	test("/billing forwards to the Billing tab and reports a cancelled checkout", async ({
		page,
		workspace: _workspace,
	}) => {
		await page.goto("/billing?checkout=cancel");
		await expect(page).toHaveURL(/\/dashboard\/\d+\/user-settings\/billing$/);
		await expect(page.getByText("Checkout was cancelled. Nothing was charged.")).toBeVisible();
		await expect(page.getByTestId("billing-current-plan")).toBeVisible();
	});

	test("/billing?crypto=done (the BTCPay return) forwards to the Billing tab with a toast", async ({
		page,
		workspace: _workspace,
	}) => {
		await page.goto("/billing?crypto=done");
		await expect(page).toHaveURL(/\/dashboard\/\d+\/user-settings\/billing$/);
		await expect(
			page.getByText("Payment received. Your credits appear once the network confirms it.")
		).toBeVisible();
		await expect(page.getByTestId("billing-current-plan")).toBeVisible();
	});
});
