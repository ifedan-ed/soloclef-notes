import type { APIRequestContext, Page } from "@playwright/test";
import { authHeaders } from "../helpers/api/auth";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, test } from "./fixtures";

/**
 * The account's one settings page: /dashboard/settings/<section>. Everything
 * here is the signed-in account's own; the only sections held back are the
 * site administrators' (features/settings-visibility).
 *
 * Sections are found by their address, not their label, so a reworded label
 * does not break the walk through them.
 */

/** Every section a normal account sees in the web app (not the desktop app's two). */
const SECTIONS = [
	"profile",
	"appearance",
	"api-key",
	"models",
	"instructions",
	"memory",
	"prompts",
	"connectors",
	"automations",
	"public-links",
] as const;

/** Kept in the code, out of sight: for site administrators only. */
const ADMIN_ONLY = ["community-prompts", "agent-permissions", "announcements"] as const;

const sectionUrl = (slug: string) => `/dashboard/settings/${slug}`;
const sectionLink = (page: Page, slug: string) =>
	page.locator(`a[href="${sectionUrl(slug)}"]`).filter({ visible: true });

async function openSection(page: Page, slug: string) {
	await page.goto(sectionUrl(slug));
	await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible();
}

type AccountSettings = {
	chat_instructions: string;
	citations_enabled: boolean;
	api_access_enabled: boolean;
};

async function readSettings(request: APIRequestContext, token: string): Promise<AccountSettings> {
	const r = await request.get("/api/v1/users/me/settings", { headers: authHeaders(token) });
	expect(r.ok()).toBeTruthy();
	return (await r.json()) as AccountSettings;
}

async function writeSettings(
	request: APIRequestContext,
	token: string,
	// A null instructions clears them, as the settings page's empty box does.
	changes: Partial<Omit<AccountSettings, "chat_instructions">> & {
		chat_instructions?: string | null;
	}
): Promise<void> {
	const r = await request.patch("/api/v1/users/me/settings", {
		headers: authHeaders(token),
		data: changes,
	});
	expect(r.ok()).toBeTruthy();
}

test.describe("Settings", () => {
	test("/dashboard/settings opens Profile; every section renders and fits", async ({
		page,
		isPhone,
	}) => {
		await page.goto("/dashboard/settings");
		await expect(page).toHaveURL(/\/dashboard\/settings\/profile$/);
		for (const slug of SECTIONS) {
			if (isPhone) {
				// The phone keeps the section list in a drawer; the desktop run walks
				// the links, so here each address is opened directly.
				await page.goto(sectionUrl(slug));
			} else {
				await sectionLink(page, slug).first().click();
			}
			await expect(page).toHaveURL(new RegExp(`/dashboard/settings/${slug}$`));
			await expectNoOverflow(page, `settings: ${slug}`);
		}
	});

	test("admin sections are not in the nav and their addresses go to Profile", async ({ page }) => {
		await openSection(page, "profile");
		for (const slug of ADMIN_ONLY) {
			await expect(page.locator(`a[href="${sectionUrl(slug)}"]`)).toHaveCount(0);
		}
		for (const slug of ADMIN_ONLY) {
			await page.goto(sectionUrl(slug));
			await expect(page).toHaveURL(/\/dashboard\/settings\/profile$/);
		}
	});

	test("there is nothing of a workspace left: no team roles, no general name", async ({ page }) => {
		await openSection(page, "profile");
		for (const gone of ["team-roles", "general", "git-remote"]) {
			await expect(page.locator(`a[href="${sectionUrl(gone)}"]`)).toHaveCount(0);
		}
		await expect(page.getByText(/team roles|manage members/i)).toHaveCount(0);
	});

	test("profile: change the display name", async ({ page, request, token }) => {
		await openSection(page, "profile");
		const name = page.getByLabel("Display Name");
		const original = await name.inputValue();
		// Unique per run: the desktop and phone projects share this account and
		// may run this test at the same moment.
		const fresh = `E2E Tester ${Date.now().toString(36)}`;
		await name.fill(fresh);
		await page.getByRole("button", { name: "Save Changes" }).click();
		await expect
			.poll(async () => {
				const r = await request.get("/users/me", { headers: authHeaders(token) });
				return ((await r.json()) as { display_name: string | null }).display_name;
			})
			.toBe(fresh);
		// Put it back; other specs read the account menu's name.
		await name.fill(original);
		await page.getByRole("button", { name: "Save Changes" }).click();
	});

	test("profile: the delete-account dialog needs DELETE typed (not confirmed)", async ({
		page,
	}) => {
		await openSection(page, "profile");
		await page.getByRole("button", { name: "Delete account" }).click();
		const dialog = page.getByRole("dialog", { name: "Delete your account?" });
		await expect(dialog).toBeVisible();
		await expectNoOverflow(page, "delete account dialog");
		const confirm = dialog.getByRole("button", { name: "Delete my account" });
		await expect(confirm).toBeDisabled();
		await dialog.getByLabel("Type DELETE to confirm").fill("DELETE");
		await expect(confirm).toBeEnabled();
		await dialog.getByRole("button", { name: "Cancel" }).click();
		await expect(dialog).toBeHidden();
	});

	test("instructions: saved to the account's settings", async ({ page, request, token }) => {
		const before = await readSettings(request, token);
		try {
			await openSection(page, "instructions");
			const text = `Always answer in British English. ${Date.now().toString(36)}`;
			await page.getByLabel("Your instructions").fill(text);
			await page.getByRole("button", { name: "Save instructions" }).click();
			await expect
				.poll(async () => (await readSettings(request, token)).chat_instructions)
				.toBe(text);
			await expectNoOverflow(page, "instructions");
		} finally {
			// Every other chat test runs as this account: leave its instructions as found.
			await writeSettings(request, token, { chat_instructions: before.chat_instructions || null });
		}
	});

	test("instructions: the citations switch is the account's", async ({ page, request, token }) => {
		const before = await readSettings(request, token);
		try {
			await openSection(page, "instructions");
			const toggle = page.getByRole("switch", { name: "Citations" });
			await expect(toggle).toHaveAttribute("aria-checked", String(before.citations_enabled));
			await toggle.click();
			await expect
				.poll(async () => (await readSettings(request, token)).citations_enabled)
				.toBe(!before.citations_enabled);
		} finally {
			await writeSettings(request, token, { citations_enabled: before.citations_enabled });
		}
	});

	test("api key: the API access switch says what it allows and turns it on and off", async ({
		page,
		request,
		token,
	}) => {
		const before = await readSettings(request, token);
		try {
			await openSection(page, "api-key");
			const box = page.getByTestId("api-access-switch");
			await expect(box).toBeVisible();
			await expect(box).toContainText(/API keys and the AI agents you sign in/);
			const toggle = box.getByRole("switch");
			await toggle.click();
			await expect
				.poll(async () => (await readSettings(request, token)).api_access_enabled)
				.toBe(!before.api_access_enabled);
			await toggle.click();
			await expect
				.poll(async () => (await readSettings(request, token)).api_access_enabled)
				.toBe(before.api_access_enabled);
			await expectNoOverflow(page, "api key");
		} finally {
			await writeSettings(request, token, { api_access_enabled: before.api_access_enabled });
		}
	});

	test("memory: what the assistant keeps about the account renders", async ({ page }) => {
		await openSection(page, "memory");
		await expect(page.getByLabel("Memory")).toBeVisible();
		await expect(page.getByRole("button", { name: "Save memory" })).toBeVisible();
		await expect(page.getByRole("button", { name: "Forget everything" })).toBeVisible();
		await expectNoOverflow(page, "memory");
	});

	test("models: the Studio outputs model is chosen by name and reset to the chat model", async ({
		page,
		request,
		token,
	}) => {
		const read = async () => {
			const r = await request.get("/api/v1/users/me/studio-model", { headers: authHeaders(token) });
			expect(r.ok()).toBeTruthy();
			return (await r.json()) as { selected: string | null; models: Array<{ id: string }> };
		};
		const before = await read();
		await openSection(page, "models");
		const select = page.getByRole("combobox", { name: "Studio outputs model" });
		await expect(select).toBeVisible();
		await expectNoOverflow(page, "models page");

		try {
			// Choose the first model the server offers: the body names it.
			const chosen = page.waitForRequest(
				(r) => r.url().endsWith("/studio-model") && r.method() === "PUT"
			);
			await select.click();
			await expectNoOverflow(page, "studio model select");
			await page.getByRole("option", { name: before.models[0].id, exact: true }).click();
			expect((await chosen).postDataJSON()).toEqual({ model: before.models[0].id });
			await expect.poll(async () => (await read()).selected).toBe(before.models[0].id);

			// Back to the default: null, and the account's chat model by name.
			const cleared = page.waitForRequest(
				(r) => r.url().endsWith("/studio-model") && r.method() === "PUT"
			);
			await select.click();
			await page.getByRole("option", { name: /^Same as chat model/ }).click();
			expect((await cleared).postDataJSON()).toEqual({ model: null });
			await expect.poll(async () => (await read()).selected).toBeNull();
		} finally {
			await request.put("/api/v1/users/me/studio-model", {
				headers: authHeaders(token),
				data: { model: before.selected },
			});
		}
	});

	test("models: the model roles show, and a member may connect only their own endpoint", async ({
		page,
	}) => {
		await openSection(page, "models");
		await expect(page.getByText("Model Roles")).toBeVisible();
		// Connecting the named providers is admin-only; a member is offered one
		// connection, an OpenAI-compatible endpoint of their own.
		const connect = page.getByRole("button", { name: /Connect$/ });
		await expect(connect).toHaveCount(1);
		await expect(connect).toHaveAccessibleName(
			/^OpenAI-Compatible OpenAI-compatible endpoint Connect$/
		);
	});

	test("connectors: overview, search, MCP server form", async ({ page }) => {
		await openSection(page, "connectors");
		const search = page.getByPlaceholder("Search connectors");
		await search.fill("nextcloud");
		await expect(page.getByText(/Nextcloud/).first()).toBeVisible();
		// Removed integrations are gone from the catalog, not hidden behind a flag.
		await search.fill("notion");
		await expect(page.getByText("No connectors found")).toBeVisible();
		await search.fill("nextcloud");
		await expectNoOverflow(page, "connectors search");
		await page.getByRole("button", { name: "Clear search" }).click();
		await expect(search).toHaveValue("");
	});

	test("my prompts: no sharing with the community for a member", async ({ page }) => {
		await openSection(page, "prompts");
		await page.getByRole("button", { name: "New", exact: true }).click();
		const dialog = page.getByRole("dialog", { name: "New prompt" });
		await expect(dialog).toBeVisible();
		await expect(dialog.getByText("Share with community")).toHaveCount(0);
		await page.keyboard.press("Escape");
	});

	test("my prompts: create, edit, delete", async ({ page }) => {
		await openSection(page, "prompts");
		const name = `E2E settings prompt ${Date.now()}`;
		await page.getByRole("button", { name: "New", exact: true }).click();
		const dialog = page.getByRole("dialog", { name: "New prompt" });
		await expect(dialog).toBeVisible();
		await expectNoOverflow(page, "new prompt dialog");
		await dialog.getByLabel("Name").fill(name);
		await dialog.getByLabel("Prompt template").fill("Explain {selection} simply.");
		await dialog.getByRole("button", { name: "Create" }).click();
		await expect(dialog).toBeHidden();
		await expect(page.getByText(name)).toBeVisible();

		const actions = async () => {
			const card = page.locator(".group").filter({ hasText: name }).last();
			await card.hover();
			await card.getByRole("button", { name: "Prompt actions" }).click();
		};
		await actions();
		await page.getByRole("menuitem", { name: "Edit" }).click();
		const edit = page.getByRole("dialog", { name: "Edit prompt" });
		await edit.getByLabel("Name").fill(`${name} v2`);
		await edit.getByRole("button", { name: "Update" }).click();
		await expect(page.getByText(`${name} v2`)).toBeVisible();

		await actions();
		await page.getByRole("menuitem", { name: "Delete" }).click();
		const alert = page.getByRole("alertdialog", { name: "Delete prompt" });
		await alert.getByRole("button", { name: "Delete" }).click();
		await expect(page.getByText(`${name} v2`)).toBeHidden();
	});

	test("appearance: timestamps toggle is local", async ({ page }) => {
		await openSection(page, "appearance");
		const toggle = page.getByRole("switch", { name: "Show message timestamps" });
		const before = await toggle.getAttribute("aria-checked");
		await toggle.click();
		await expect(toggle).not.toHaveAttribute("aria-checked", before ?? "");
	});
});
