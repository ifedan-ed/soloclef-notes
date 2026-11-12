import { authHeaders, BACKEND_URL } from "../helpers/api/auth";
import { expect, test } from "./fixtures";

/**
 * A scheduled automation fires on its own: Celery beat ticks the schedule
 * selector every minute, the worker runs the plan (with the stack's fake
 * model), and a run is recorded. Nothing in the UI asks anyone to set one up.
 */

type Run = { id: number; status: string };

test("a schedule trigger fires and the run finishes", async ({ request, token }) => {
	test.setTimeout(240_000);
	const created = await request.post(`${BACKEND_URL}/api/v1/automations`, {
		headers: authHeaders(token),
		data: {
			name: "E2E every minute",
			definition: {
				name: "E2E every minute",
				plan: [{ step_id: "s1", action: "agent_task", params: { query: "Say hello." } }],
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
		.toMatch(/^(succeeded|failed|timed_out|cancelled)$/);
	expect(last?.status, "the scheduled run should succeed").toBe("succeeded");
});

// The account's automations are shared by every test of the run, so this
// does not count them: it checks the home never asks anyone to make one.
test("the notebooks home never asks for an automation", async ({ page, request, token }) => {
	const list = await request.get(`${BACKEND_URL}/api/v1/automations`, {
		headers: authHeaders(token),
	});
	expect(list.ok(), await list.text()).toBeTruthy();
	await page.goto("/dashboard");
	await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible();
	await expect(page.getByText(/automation/i)).toHaveCount(0);
});
