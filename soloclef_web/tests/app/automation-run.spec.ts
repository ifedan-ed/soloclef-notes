import type { APIRequestContext } from "@playwright/test";
import { authHeaders, BACKEND_URL } from "../helpers/api/auth";
import { createNotebook } from "../helpers/api/notebooks";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, test } from "./fixtures";

/**
 * An automation run, end to end: Celery beat fires the schedule, the worker
 * runs the agent with the stack's scripted model (tests/e2e/fakes/chat_llm.py),
 * and the run page shows the result.
 *
 * - "List my notebooks" is answered from the notebooks (the automation-only
 *   list_notebooks tool), not from the Studio files in /documents.
 * - A task that needs the web while its automation may only read documents
 *   ends "Needs attention" with the agent's reason, not "Succeeded".
 */

type Run = { id: number; status: string };
const SHOTS = process.env.E2E_SCREENSHOT_DIR;

async function scheduledAutomation(
	request: APIRequestContext,
	token: string,
	workspaceId: number,
	name: string,
	query: string
): Promise<number> {
	const created = await request.post(`${BACKEND_URL}/api/v1/automations`, {
		headers: authHeaders(token),
		data: {
			workspace_id: workspaceId,
			name,
			definition: {
				name,
				plan: [{ step_id: "s1", action: "agent_task", params: { query, auto_approve_all: true } }],
			},
		},
	});
	expect(created.ok(), await created.text()).toBeTruthy();
	const id = ((await created.json()) as { id: number }).id;
	const trigger = await request.post(`${BACKEND_URL}/api/v1/automations/${id}/triggers`, {
		headers: authHeaders(token),
		data: { type: "schedule", params: { cron: "* * * * *", timezone: "UTC" } },
	});
	expect(trigger.ok(), await trigger.text()).toBeTruthy();
	return id;
}

async function finishedRun(request: APIRequestContext, token: string, id: number): Promise<Run> {
	let last: Run | undefined;
	await expect
		.poll(
			async () => {
				const r = await request.get(`${BACKEND_URL}/api/v1/automations/${id}/runs`, {
					headers: authHeaders(token),
				});
				last = ((await r.json()) as { items: Run[] }).items[0];
				return last?.status ?? "none";
			},
			{ timeout: 210_000, intervals: [5_000] }
		)
		.toMatch(/^(succeeded|failed|needs_attention|timed_out|cancelled)$/);
	return last as Run;
}

test("a run lists the notebooks, and one that needs the web says so", async ({
	page,
	request,
	token,
	workspace,
}, testInfo) => {
	test.skip(testInfo.project.name !== "desktop", "one scheduled run is enough");
	test.setTimeout(300_000);
	await createNotebook(request, token, workspace.id, "Grant ideas");
	await createNotebook(request, token, workspace.id, "Reading list");

	const listing = await scheduledAutomation(
		request,
		token,
		workspace.id,
		"E2E list notebooks",
		"E2E_AUTOMATION_NOTEBOOKS List the titles of my notebooks."
	);
	const needsWeb = await scheduledAutomation(
		request,
		token,
		workspace.id,
		"E2E needs the web",
		"E2E_AUTOMATION_NEEDS_WEB Summarise https://example.org/report."
	);

	expect((await finishedRun(request, token, listing)).status).toBe("succeeded");
	expect((await finishedRun(request, token, needsWeb)).status).toBe("needs_attention");

	await page.goto(`/dashboard/${workspace.id}/automations/${listing}`);
	await expect(page.getByRole("heading", { name: "E2E list notebooks", level: 1 })).toBeVisible();
	await page.getByText("Succeeded").first().click();
	const answer = page.getByText(/E2E automation notebooks:/);
	await expect(answer).toBeVisible();
	await expect(answer).toContainText("Grant ideas");
	await expect(answer).toContainText("Reading list");
	await expectNoOverflow(page, "automation run: succeeded");
	if (SHOTS) await page.screenshot({ path: `${SHOTS}/04-run-lists-notebooks.png`, fullPage: true });

	await page.goto(`/dashboard/${workspace.id}/automations/${needsWeb}`);
	await expect(page.getByRole("heading", { name: "E2E needs the web", level: 1 })).toBeVisible();
	await page.getByText("Needs attention").first().click();
	await expect(page.getByText("Needs your attention").first()).toBeVisible();
	await expect(page.getByText(/this automation may only read documents/).first()).toBeVisible();
	await expectNoOverflow(page, "automation run: needs attention");
	if (SHOTS) await page.screenshot({ path: `${SHOTS}/05-run-needs-attention.png`, fullPage: true });

	// Deleting it from its own page leaves no "not found" toast behind.
	await page.getByRole("button", { name: "Delete" }).click();
	await page
		.getByRole("alertdialog", { name: "Delete this automation?" })
		.getByRole("button", { name: "Delete" })
		.click();
	await expect(page.getByText("Automation deleted")).toBeVisible();
	await page.waitForTimeout(2_000);
	await expect(page.getByText(/not found/i)).toHaveCount(0);
});

test("the form offers Auto first and picks it by default", async ({
	page,
	workspace,
}, testInfo) => {
	await page.goto(`/dashboard/${workspace.id}/automations/new`);
	await expect(page.getByRole("heading", { name: "New automation", level: 1 })).toBeVisible();
	const chat = page.getByRole("combobox", { name: "Chat model" });
	await expect(chat).toContainText("Auto");
	await chat.click();
	const auto = page.getByRole("option", { name: /Auto/ }).first();
	await expect(auto).toBeVisible();
	await expect(auto).toContainText("Picks a standard model for each run");
	await expectNoOverflow(page, "automation form: chat model menu");
	if (SHOTS && testInfo.project.name === "desktop") {
		await page.screenshot({ path: `${SHOTS}/03-automation-form-auto.png` });
	}
	await page.keyboard.press("Escape");

	// Each task says what it may read and which notebook it answers from.
	await expect(page.getByRole("combobox", { name: "Can use" })).toContainText("Your documents");
	await expect(page.getByRole("combobox", { name: "Notebook" })).toContainText("Any notebook");
	await page.getByRole("combobox", { name: "Can use" }).click();
	await page.getByRole("option", { name: "Your documents and the web" }).click();
	await expect(page.getByText(/Can also search the web and read pages/)).toBeVisible();
	await expectNoOverflow(page, "automation form: can use");
});
