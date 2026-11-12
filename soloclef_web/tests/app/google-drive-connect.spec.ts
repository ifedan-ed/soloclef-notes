import type { Page } from "@playwright/test";
import { createNotebook } from "../helpers/api/notebooks";
import { signInAsNewAccount } from "../helpers/api/own-account";
import { sendGoogleConsentToStandIn } from "../helpers/google/consent-stand-in";
import { installFakeGooglePicker } from "../helpers/google/fake-picker";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, openNotebook, test, uniqueName } from "./fixtures";

/**
 * Google Drive end to end, against the stand-in Google of the e2e stack
 * (tests/e2e/fakes/google_server.py, its consent page at /__google__) and a
 * stand-in Google Picker served in place of apis.google.com/js/api.js.
 * Connect goes to the consent page and back; picked files are imported,
 * parsed and listed as notebook sources; Disconnect revokes the grant.
 */

const ACCOUNT = "e2e.drive@example.com";

type StubState = { revoked: string[]; drive_requests: string[] };

async function stubState(page: Page): Promise<StubState> {
	const response = await page.request.get("/__google__/__stub__/state");
	return (await response.json()) as StubState;
}

async function allowOnConsentPage(page: Page) {
	await expect(page).toHaveURL(/\/__google__\/o\/oauth2\/auth/);
	// Only the one non-sensitive scope is asked for.
	await expect(page.getByTestId("scopes")).toHaveText("https://www.googleapis.com/auth/drive.file");
	await page.getByRole("button", { name: "Allow" }).click();
}

test.describe("Google Drive connect", () => {
	// An account has one Drive connection, and the desktop and phone runs of
	// these tests connect and disconnect at the same time: each test signs
	// in as an account of its own (helpers/api/own-account.ts), which starts
	// unconnected, so no test can see another's half-done connection.
	test.beforeEach(({ page }) => sendGoogleConsentToStandIn(page));

	test("the catalog connects Drive through Google's consent, then manages it", async ({
		page,
		request,
	}) => {
		await signInAsNewAccount(page, request, "drive");
		await page.goto("/dashboard/settings/connectors");
		const card = page.getByTestId("google-drive-connector-card");
		await expect(card).toContainText("Pick files from your Google Drive");
		await expectNoOverflow(page, "connectors catalog with Google Drive");

		await card.getByRole("button", { name: "Connect", exact: true }).click();
		await allowOnConsentPage(page);

		await expect(page).toHaveURL(/\/dashboard\/settings\/connectors/);
		await expect(page).not.toHaveURL(/google_drive=/);
		const connected = page.getByTestId("google-drive-connector-card");
		await expect(connected).toContainText(`Connected as ${ACCOUNT}`);

		await connected.getByRole("button", { name: "Manage" }).click();
		await expect(page.getByTestId("google-drive-account")).toHaveText(`Connected as ${ACCOUNT}`);
	});

	test("Add sources connects, picks files in the Picker and lists them; Disconnect revokes", async ({
		page,
		request,
		isPhone,
	}) => {
		const me = await signInAsNewAccount(page, request, "drive");
		const notebook = await createNotebook(request, me.token, uniqueName("E2E Drive notebook"));
		await installFakeGooglePicker(page);
		await openNotebook(page, notebook.id, "Sources");
		await page.getByRole("button", { name: "Add sources" }).first().click();
		let dialog = page.getByRole("dialog", { name: "Add sources" });
		await dialog.getByRole("tab", { name: /connector/i }).click();
		const drive = dialog.getByTestId("google-drive-sources");
		await drive.getByRole("button", { name: "Connect", exact: true }).click();
		await allowOnConsentPage(page);

		// Back on the notebook, with Add sources reopened where the picker is.
		dialog = page.getByRole("dialog", { name: "Add sources" });
		await expect(dialog.getByTestId("google-drive-account")).toHaveText(`Connected as ${ACCOUNT}`);
		await expectNoOverflow(page, "Add sources: Google Drive connected");

		await dialog.getByTestId("google-drive-pick").click();
		let picker = page.getByRole("dialog", { name: "Google Picker" });
		// Only a folder: nothing is sent, and the page says why.
		await picker.getByLabel("A folder").check();
		await picker.getByRole("button", { name: "Select" }).click();
		await expect(page.getByText("No files were picked.")).toBeVisible();

		await dialog.getByTestId("google-drive-pick").click();
		picker = page.getByRole("dialog", { name: "Google Picker" });
		await picker.getByLabel("Drive field notes.md").check();
		await picker.getByLabel("Research plan.txt").check();
		const importing = page.waitForRequest(
			(request) => request.method() === "POST" && /\/google-drive\/import$/.test(request.url())
		);
		await picker.getByRole("button", { name: "Select" }).click();

		// The picks land in the account's library and this notebook; the body
		// names the notebook and no owner (the session is the owner).
		const sent = await importing;
		expect(sent.postDataJSON()).toEqual({
			files: [
				{ id: "e2eDriveNotes0001", name: "Drive field notes.md", mime_type: "text/markdown" },
				{ id: "e2eDrivePlan00002", name: "Research plan.txt", mime_type: "text/plain" },
			],
			notebook_id: notebook.id,
		});
		expect((await sent.response())?.status()).toBe(200);

		const built = await page.evaluate(
			() => (window as unknown as { __pickerBuilt: Record<string, unknown> }).__pickerBuilt
		);
		// setAppId is what grants each picked file to the app under drive.file.
		expect(built.appId).toBe("123456789012");
		expect(built.apiKey).toBe("e2e-picker-key");
		expect(String(built.token)).toMatch(/^ya29\.stub-/);
		expect(built.views).toEqual([
			expect.objectContaining({ includeFolders: true, selectFolders: false }),
		]);

		await expect(page.getByText("2 files are being added")).toBeVisible();
		await page.keyboard.press("Escape");
		if (isPhone) await page.getByRole("tab", { name: /^Sources/ }).click();
		await expect(page.getByText("Drive field notes.md").first()).toBeVisible();
		await expect(page.getByText("Research plan.txt").first()).toBeVisible();

		// The worker downloaded both through the stand-in Drive API.
		await expect
			.poll(async () => (await stubState(page)).drive_requests, { timeout: 60_000 })
			.toEqual(
				expect.arrayContaining([
					"files/e2eDriveNotes0001?alt=media",
					"files/e2eDrivePlan00002?alt=media",
				])
			);

		await page.getByRole("button", { name: "Add sources" }).first().click();
		dialog = page.getByRole("dialog", { name: "Add sources" });
		await dialog.getByRole("tab", { name: /connector/i }).click();
		const before = (await stubState(page)).revoked.length;
		const panel = dialog.getByTestId("google-drive-sources");
		await panel.getByRole("button", { name: "Disconnect", exact: true }).click();
		await panel.getByRole("button", { name: "Disconnect", exact: true }).last().click();
		await expect(panel.getByRole("button", { name: "Connect", exact: true })).toBeVisible();
		await expect.poll(async () => (await stubState(page)).revoked.length).toBeGreaterThan(before);
	});
});
