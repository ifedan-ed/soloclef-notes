import type { APIRequestContext } from "@playwright/test";
import { authHeaders, BACKEND_URL } from "../helpers/api/auth";
import { getNote, type NoteMaker, provideNotes } from "../helpers/api/notes";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, test as base } from "./fixtures";

// A note the run can find, deleted with the test, and the automations the
// test made, deleted with it too (a failed assertion must not leave a
// schedule firing every minute into the tests that follow).
const test = base.extend<{ notes: NoteMaker; cleanup: number[] }>({
	notes: ({ request, token }, use) => provideNotes(request, token, use),
	cleanup: async ({ request, token }, use) => {
		const ids: number[] = [];
		await use(ids);
		for (const id of ids) {
			// 404: the test deleted it itself, from its page.
			await request.delete(`${BACKEND_URL}/api/v1/automations/${id}`, {
				headers: authHeaders(token),
			});
		}
	},
});

/**
 * An automation run, end to end: the worker's schedule tick fires it, the
 * worker runs one headless turn of the chat's agent against the stack's
 * stand-in model (docker/e2e-web-go/model-stub), and the run page shows the
 * result.
 *
 * - A task that reads the documents is answered from them: the stand-in
 *   calls the product's real search and lists the canary it returned.
 * - A task that asks for a web page nobody is present to approve does not
 *   end "Succeeded": the page reader always asks, a run has nobody to ask,
 *   so the call is refused and the run ends "Needs attention" naming the
 *   tool. (The Python tier left that to the model, which had to call a
 *   report_outcome tool to say so; a model that forgot reported success.)
 */

type Run = { id: number; status: string };
const SHOTS = process.env.E2E_SCREENSHOT_DIR;

async function scheduledAutomation(
	request: APIRequestContext,
	token: string,
	name: string,
	query: string,
	scope: "documents" | "all" = "documents"
): Promise<number> {
	const created = await request.post(`${BACKEND_URL}/api/v1/automations`, {
		headers: authHeaders(token),
		data: {
			name,
			definition: {
				name,
				plan: [
					{
						step_id: "s1",
						action: "agent_task",
						params: { query, auto_approve_all: true, retrieval_scope: scope },
					},
				],
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

test("a run answers from the documents, and one refused a web page says so", async ({
	page,
	request,
	token,
	notes,
	cleanup,
}, testInfo) => {
	test.skip(testInfo.project.name !== "desktop", "one scheduled run is enough");
	test.setTimeout(300_000);
	// A word and a canary only this run wrote: the account's documents are
	// every test's, and the run searches all of them.
	const word = `okapi${Date.now().toString(36)}`;
	const canary = `SCOPE-AUTO-${Math.floor(1000 + Math.random() * 9000)}`;
	const note = await notes.create({
		title: "Automation field notes",
		content: `The ${word} survey's canary is ${canary}.`,
	});
	await expect
		.poll(async () => (await getNote(request, token, note.id)).index_state, { timeout: 60_000 })
		.toBe("ready");

	const reading = await scheduledAutomation(
		request,
		token,
		"E2E read the documents",
		`E2E_KB_SCOPE ${word}`
	);
	const needsWeb = await scheduledAutomation(
		request,
		token,
		"E2E needs the web",
		"E2E_AUTOMATION_NEEDS_WEB Summarise https://example.org/report.",
		"all"
	);

	// Both fire every minute for as long as they exist: whatever happens
	// below, neither outlives the test.
	cleanup.push(reading, needsWeb);

	expect((await finishedRun(request, token, reading)).status).toBe("succeeded");
	expect((await finishedRun(request, token, needsWeb)).status).toBe("needs_attention");

	await page.goto(`/dashboard/automations/${reading}`);
	await expect(
		page.getByRole("heading", { name: "E2E read the documents", level: 1 })
	).toBeVisible();
	await page.getByText("Succeeded").first().click();
	const answer = page.getByText(/E2E scope answer:/);
	await expect(answer).toBeVisible();
	await expect(answer).toContainText(canary);
	await expectNoOverflow(page, "automation run: succeeded");
	if (SHOTS) await page.screenshot({ path: `${SHOTS}/04-run-reads-documents.png`, fullPage: true });

	await page.goto(`/dashboard/automations/${needsWeb}`);
	await expect(page.getByRole("heading", { name: "E2E needs the web", level: 1 })).toBeVisible();
	await page.getByText("Needs attention").first().click();
	await expect(page.getByText("Needs your attention").first()).toBeVisible();
	// The run's own reason: which tool nobody was there to approve.
	await expect(page.getByText(/Nobody was present to approve: web_crawl/).first()).toBeVisible();
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

test("the form runs on the chat's model unless one is picked", async ({ page }, testInfo) => {
	await page.goto("/dashboard/automations/new");
	await expect(page.getByRole("heading", { name: "New automation", level: 1 })).toBeVisible();
	// Nothing picked: runs follow the account's chat model, and the form
	// names the one that is today.
	const model = page.getByRole("combobox", { name: "Runs on" });
	await expect(model).toContainText("Your chat model");
	await expect(page.getByText(/Runs follow the model your chat uses\. Currently \S+/)).toBeVisible();
	await model.click();
	// The first row is "none picked"; under it, the models the proxy lists.
	const search = page.getByRole("textbox", { name: "Search models" });
	await expect(search).toBeVisible();
	await expect(page.getByRole("button", { name: /^Your chat model/ })).toBeVisible();
	await expectNoOverflow(page, "automation form: model menu");
	if (SHOTS && testInfo.project.name === "desktop") {
		await page.screenshot({ path: `${SHOTS}/03-automation-form-model.png` });
	}
	await page.keyboard.press("Escape");

	// Each task says what it may read and which notebook it answers from.
	await expect(page.getByRole("combobox", { name: "Can use" })).toContainText("Your documents");
	// (Exact: "Save results to notebook" is a combobox of the same task.)
	await expect(page.getByRole("combobox", { name: "Notebook", exact: true })).toContainText(
		"Any notebook"
	);
	await page.getByRole("combobox", { name: "Can use" }).click();
	await page.getByRole("option", { name: "Your documents and the web" }).click();
	await expect(page.getByText(/Can also search the web and read pages/)).toBeVisible();
	await expectNoOverflow(page, "automation form: can use");
});
