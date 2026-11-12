import type { Page } from "@playwright/test";
import { createNotebook } from "../helpers/api/notebooks";
import { signInAsNewAccount } from "../helpers/api/own-account";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, openNotebook, test, uniqueName } from "./fixtures";

/**
 * Connecting Nextcloud in one click, against the stand-in Nextcloud of the
 * e2e stack (tests/e2e/fakes/nextcloud_server.py, reached at /__nextcloud__).
 * The sign-in opens in a new tab; granting access there connects this page.
 */

// The stand-in's address as the API was given it, which the page shows. The
// stack names every service by its container (docker-compose.go.yml says why).
const SERVER = "soloclefgo-e2e-nextcloud-stub-1:8080";

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
	// An account has one Nextcloud connection, and the desktop and phone runs
	// of these tests connect and disconnect at the same time: each test signs
	// in as an account of its own (helpers/api/own-account.ts), which starts
	// unconnected, so no test can see another's half-done connection.
	test("the connectors catalog connects Nextcloud next to Google Drive", async ({
		page,
		request,
	}) => {
		await signInAsNewAccount(page, request, "nextcloud");
		await page.goto("/dashboard/settings/connectors");
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

	test("a started sign-in can be cancelled", async ({ page, request }) => {
		await signInAsNewAccount(page, request, "nextcloud");
		await page.goto("/dashboard/settings/connectors");
		const card = page.getByTestId("nextcloud-connector-card");
		await card.getByRole("button", { name: "Connect", exact: true }).click();
		await expect(card.getByRole("link", { name: `Sign in to ${SERVER}` })).toBeVisible();
		await card.getByRole("button", { name: "Cancel" }).click();
		await expect(card.getByRole("link", { name: `Sign in to ${SERVER}` })).toHaveCount(0);
		await expect(card.getByRole("button", { name: "Connect", exact: true })).toBeEnabled();
	});

	test("Add sources connects in place, imports a file and a folder; Disconnect revokes", async ({
		page,
		request,
		isPhone,
	}) => {
		const me = await signInAsNewAccount(page, request, "nextcloud");
		const notebook = await createNotebook(request, me.token, uniqueName("E2E Nextcloud notebook"));
		const openConnectorTab = async () => {
			await page.getByRole("button", { name: "Add sources" }).first().click();
			const opened = page.getByRole("dialog", { name: "Add sources" });
			await opened.getByRole("tab", { name: /connector/i }).click();
			return opened;
		};
		const posted = (path: RegExp) =>
			page.waitForRequest((request) => request.method() === "POST" && path.test(request.url()));
		const showSources = async () => {
			if (isPhone) await page.getByRole("tab", { name: /^Sources/ }).click();
		};

		await openNotebook(page, notebook.id, "Sources");
		let dialog = await openConnectorTab();
		// Google Drive sits above Nextcloud here; each has its own Connect.
		let nextcloud = dialog.getByTestId("nextcloud-sources");
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

		// A loose file imports straight away, into the account's library and
		// this notebook; the body names no owner (the session is the owner).
		await nextcloud.getByRole("checkbox", { name: "Select Welcome.md" }).check();
		let importing = posted(/\/nextcloud\/import$/);
		await nextcloud.getByRole("button", { name: "Add 1 source" }).click();
		let sent = await importing;
		expect(sent.postDataJSON()).toEqual({
			items: [{ path: "/Welcome.md", kind: "file" }],
			parser: "local",
			use_vision_llm: false,
			notebook_id: notebook.id,
		});
		expect((await sent.response())?.status()).toBe(200);
		await expect(page.getByText("Adding 1 source")).toBeVisible();
		// The dialog closes itself after an import.
		await expect(dialog).toBeHidden();
		await showSources();
		await expect(page.getByText("Welcome.md").first()).toBeVisible();

		// A folder goes through the confirm step first; its preview request
		// takes the same body.
		dialog = await openConnectorTab();
		nextcloud = dialog.getByTestId("nextcloud-sources");
		await nextcloud.getByRole("checkbox", { name: "Select folder Field notes" }).check();
		const previewing = posted(/\/nextcloud\/import\/preview$/);
		await nextcloud.getByRole("button", { name: "Add 1 source" }).click();
		const previewed = await previewing;
		expect(previewed.postDataJSON()).toMatchObject({
			items: [{ path: "/Field notes", kind: "folder" }],
		});
		expect((await previewed.response())?.status()).toBe(200);
		importing = posted(/\/nextcloud\/import$/);
		await nextcloud.getByRole("button", { name: "Import 1 file" }).click();
		sent = await importing;
		expect(sent.postDataJSON()).toMatchObject({
			items: [{ path: "/Field notes", kind: "folder" }],
			notebook_id: notebook.id,
		});
		expect((await sent.response())?.status()).toBe(200);
		await expect(dialog).toBeHidden();
		await showSources();
		// A picked folder is one source, listed by the folder's name.
		await expect(page.getByText("Field notes").first()).toBeVisible();

		dialog = await openConnectorTab();
		nextcloud = dialog.getByTestId("nextcloud-sources");
		const before = await revokedCount(page);
		await nextcloud.getByRole("button", { name: "Disconnect", exact: true }).click();
		await nextcloud.getByRole("button", { name: "Disconnect", exact: true }).last().click();
		await expect(nextcloud.getByRole("button", { name: "Connect", exact: true })).toBeVisible();
		await expect(dialog.getByText("Welcome.md")).toHaveCount(0);
		await expect.poll(() => revokedCount(page)).toBeGreaterThan(before);
	});
});
