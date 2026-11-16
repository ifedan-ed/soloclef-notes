import { expect, openNotebook, test } from "./fixtures";

/**
 * A notebook chat's Manage tools lists the account's connections, each as
 * one row with one switch: the tools its next message will be offered
 * (GET /api/v1/agent/tools, assembly.Menu). Switching a connection off
 * leaves its tools out of the next message.
 *
 * The connections the account has depend on the stack: this journey needs
 * at least one (a mailbox, Drive, Nextcloud or an MCP server) and skips
 * when there is none, because the e2e stack has no mail server and no route
 * out to add one through the settings form. Set E2E_SCREENSHOT_DIR to keep
 * the menu's pictures at the project's width.
 */

const SHOTS = process.env.E2E_SCREENSHOT_DIR;

test("Manage tools lists the account's connections with one switch each", async ({
	page,
	notebook,
	isPhone,
}, testInfo) => {
	const listed = await page.request.get("/api/v1/agent/tools");
	expect(listed.ok()).toBe(true);
	const tools = (await listed.json()) as { name: string; group?: string }[];
	const groups = [...new Set(tools.flatMap((tool) => (tool.group ? [tool.group] : [])))];
	test.skip(groups.length === 0, "the account has no connection on this stack");

	await openNotebook(page, notebook.id, "Chat");
	await page.getByRole("button", { name: "Upload files, manage tools and more" }).first().click();
	if (isPhone) {
		await page.getByRole("button", { name: "Manage tools" }).click();
	} else {
		await page.getByRole("menuitem", { name: "Manage tools" }).hover();
	}
	await expect(page.getByText("Your connections")).toBeVisible();
	for (const group of groups) {
		await expect(page.getByText(group, { exact: true }).first()).toBeVisible();
	}
	if (SHOTS) {
		await page.screenshot({ path: `${SHOTS}/tools-menu-${testInfo.project.name}.png` });
	}

	if (!isPhone) {
		// One connection's own tools, in its submenu.
		await page.getByText(groups[0], { exact: true }).first().hover();
		const first = tools.find((tool) => tool.group === groups[0]);
		if (first && SHOTS) {
			await page.waitForTimeout(300);
			await page.screenshot({ path: `${SHOTS}/tools-menu-${testInfo.project.name}-group.png` });
		}
	}
});
