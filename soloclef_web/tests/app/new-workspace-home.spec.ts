import { authHeaders, BACKEND_URL } from "../helpers/api/auth";
import { createWorkspace, deleteWorkspace } from "../helpers/api/workspaces";
import { expect, test } from "./fixtures";

/** A brand-new workspace chats on the shared models: no model onboarding, ever. */

test("a brand-new workspace opens on its home and is ready to chat", async ({
	page,
	request,
	token,
}) => {
	const ws = await createWorkspace(request, token, `fresh-${Date.now().toString(36)}`);
	try {
		const status = await request.get(`${BACKEND_URL}/api/v1/workspaces/${ws.id}/llm-setup-status`, {
			headers: authHeaders(token),
		});
		expect(((await status.json()) as { status: string }).status).toBe("ready");

		const visited: string[] = [];
		page.on("framenavigated", (frame) => {
			if (frame === page.mainFrame()) visited.push(frame.url());
		});
		await page.goto(`/dashboard/${ws.id}`);
		await expect(page).toHaveURL(new RegExp(`/dashboard/${ws.id}/notebooks$`));
		await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible();
		expect(visited.filter((url) => url.includes("/onboard"))).toEqual([]);
	} finally {
		await deleteWorkspace(request, token, ws.id).catch(() => undefined);
	}
});

test("an old /onboard link lands on the workspace home", async ({ page, workspace }) => {
	await page.goto(`/dashboard/${workspace.id}/onboard`);
	await expect(page).toHaveURL(new RegExp(`/dashboard/${workspace.id}/notebooks$`));
	await expect(page.getByText("Choose a model")).toHaveCount(0);
});

test("members pick among the shared models but manage no providers", async ({
	page,
	workspace,
}) => {
	await page.goto(`/dashboard/${workspace.id}/workspace-settings/models`);
	await expect(page.getByText("Model Roles")).toBeVisible();
	await expect(page.getByRole("button", { name: /OpenAI.*Connect|Connect.*OpenAI/ })).toHaveCount(
		0
	);
});

test("creating a workspace from the switcher opens its own home and stays there", async ({
	page,
	workspace,
	request,
	token,
	isPhone,
}) => {
	test.skip(isPhone, "the workspace rail is a desktop control");
	await page.goto(`/dashboard/${workspace.id}/notebooks`);
	await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible();
	await page.getByRole("button", { name: "Add workspace" }).click();
	const dialog = page.getByRole("dialog", { name: "Create Workspace" });
	await dialog.getByLabel("Name").fill(`Ward ${Date.now().toString(36)}`);
	const response = page.waitForResponse(
		(r) => r.url().endsWith("/api/v1/workspaces") && r.request().method() === "POST"
	);
	await dialog.getByRole("button", { name: "Create" }).click();
	const created = (await (await response).json()) as { id: number };
	try {
		await expect(page).toHaveURL(new RegExp(`/dashboard/${created.id}/notebooks$`));
		await page.waitForTimeout(2_000);
		await expect(page).toHaveURL(new RegExp(`/dashboard/${created.id}/notebooks$`));
		await expect(page.getByText("No notebooks yet").first()).toBeVisible();
	} finally {
		await deleteWorkspace(request, token, created.id).catch(() => undefined);
	}
});
