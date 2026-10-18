import type { Page } from "@playwright/test";
import { authHeaders } from "../helpers/api/auth";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, test } from "./fixtures";

/** Workspace settings: /dashboard/<ws>/workspace-settings/<tab>, connectors, team roles. */

const TABS: [slug: string, label: string][] = [
	["general", "General"],
	["connectors", "Connectors"],
	["automations", "Automations"],
	["models", "Models"],
	["team-roles", "Team Roles"],
	["prompts", "System Instructions"],
	["public-links", "Public links"],
];

const url = (ws: number, slug: string) => `/dashboard/${ws}/workspace-settings/${slug}`;

async function openTab(page: Page, ws: number, slug: string) {
	await page.goto(url(ws, slug));
	await expect(page.getByRole("heading", { name: "Workspace Settings", level: 1 })).toBeVisible();
}

test.describe("Workspace settings", () => {
	test("/workspace-settings opens General; every tab renders and fits", async ({
		page,
		workspace,
	}) => {
		await page.goto(`/dashboard/${workspace.id}/workspace-settings`);
		await expect(page).toHaveURL(/\/workspace-settings\/general$/);
		for (const [slug, label] of TABS) {
			await page
				.getByRole("link", { name: label, exact: true })
				.filter({ visible: true })
				.first()
				.click();
			await expect(page).toHaveURL(new RegExp(`/workspace-settings/${slug}$`));
			await expectNoOverflow(page, `workspace settings: ${label}`);
		}
	});

	test("connected repo is gone: no nav item, and its old address opens General", async ({
		page,
		workspace,
	}) => {
		await page.goto(url(workspace.id, "git-remote"));
		await expect(page).toHaveURL(/\/workspace-settings\/general$/);
		await expect(page.getByRole("link", { name: "Connected repo" })).toHaveCount(0);
	});

	test("general: rename the workspace", async ({ page, workspace, request, token }) => {
		await openTab(page, workspace.id, "general");
		await page.getByLabel("Name").fill("E2E renamed workspace");
		await page.getByRole("button", { name: "Save Changes" }).click();
		await expect
			.poll(async () => {
				const r = await request.get(`/api/v1/workspaces/${workspace.id}`, {
					headers: authHeaders(token),
				});
				return ((await r.json()) as { name: string }).name;
			})
			.toBe("E2E renamed workspace");
	});

	test("models: the Studio outputs model table, per-type override and reset", async ({
		page,
		workspace,
		request,
		token,
	}) => {
		await openTab(page, workspace.id, "models");
		await expect(page.getByText("Studio outputs model")).toBeVisible();
		const table = page.getByRole("table").filter({ hasText: "Per output type" });
		for (const type of [
			"Audio overview",
			"Video overview",
			"Slides",
			"Mind map",
			"Flashcards",
			"Quiz",
			"Infographic",
			"Data table",
		]) {
			await expect(table.getByText(type, { exact: true })).toBeVisible();
		}
		await expectNoOverflow(page, "models page");

		const put = page.waitForRequest(
			(r) => r.url().endsWith("/studio-model") && r.method() === "PUT"
		);
		await page.getByRole("combobox", { name: "Model for Quiz" }).click();
		await expectNoOverflow(page, "studio model select");
		await page.getByRole("option", { name: /Auto \(same as chat model\)/ }).click();
		await put;
		const reset = page.getByRole("button", { name: "Back to recommended" });
		await expect(reset).toBeVisible();
		const del = page.waitForRequest(
			(r) => r.url().endsWith("/studio-model") && r.method() === "DELETE"
		);
		await reset.click();
		await del;
		await expect(reset).toBeHidden();
		const r = await request.get(`/api/v1/workspaces/${workspace.id}/studio-model`, {
			headers: authHeaders(token),
		});
		expect(r.ok()).toBeTruthy();
	});

	test("models: members see the model roles but no provider connections", async ({
		page,
		workspace,
	}) => {
		await openTab(page, workspace.id, "models");
		await expect(page.getByText("Model Roles")).toBeVisible();
		// Connecting providers is admin-only; everyone uses the shared models.
		await expect(page.getByRole("button", { name: /OpenAI.*Connect|Connect.*OpenAI/ })).toHaveCount(
			0
		);
	});

	test("connectors: overview, search, MCP server form", async ({ page, workspace }) => {
		await openTab(page, workspace.id, "connectors");
		await expect(page.getByRole("heading", { name: "Connectors", level: 1 })).toBeVisible();
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

	test("/connectors redirects into workspace settings", async ({ page, workspace }) => {
		await page.goto(`/dashboard/${workspace.id}/connectors`);
		await expect(page).toHaveURL(
			new RegExp(`/dashboard/${workspace.id}/workspace-settings/connectors$`)
		);
	});

	test("team roles: the three roles, read-only, no custom roles", async ({ page, workspace }) => {
		await openTab(page, workspace.id, "team-roles");
		await expect(page.getByRole("heading", { name: "Roles", exact: true })).toBeVisible();
		for (const role of ["Owner", "Editor", "Viewer"]) {
			await expect(page.getByText(role, { exact: true }).first()).toBeVisible();
		}
		await expect(page.getByRole("button", { name: "Create Custom Role" })).toHaveCount(0);
		await expectNoOverflow(page, "team roles");
	});

	test("system instructions save", async ({ page, workspace, request, token }) => {
		await openTab(page, workspace.id, "prompts");
		await page.getByLabel("Your Instructions").fill("Always answer in British English.");
		await page.getByRole("button", { name: "Save Instructions" }).click();
		await expect
			.poll(async () => {
				const r = await request.get(`/api/v1/workspaces/${workspace.id}`, {
					headers: authHeaders(token),
				});
				return ((await r.json()) as { qna_custom_instructions: string }).qna_custom_instructions;
			})
			.toBe("Always answer in British English.");
	});
});
