import { authHeaders } from "../helpers/api/auth";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, test } from "./fixtures";

/** Automations (list, new, detail, edit) and Logs. */

test.describe("Automations", () => {
	test("the list lives in settings, and the builder form creates an automation", async ({
		page,
		request,
		token,
	}) => {
		// The old address of the list still lands on it.
		await page.goto("/dashboard/automations");
		await expect(page).toHaveURL(/\/dashboard\/settings\/automations$/);
		// The account's automations are shared by every test of the run, so
		// the page is either the empty state or the table; both offer
		// "Create manually".
		await expect(page.getByRole("heading", { name: "Automations", level: 1 })).toBeVisible();
		await expectNoOverflow(page, "automations list");

		await page.getByRole("link", { name: "Create manually" }).first().click();
		await expect(page).toHaveURL(/\/dashboard\/automations\/new$/);
		await expect(page.getByRole("heading", { name: "New automation", level: 1 })).toBeVisible();
		const name = `E2E built digest ${Date.now().toString(36)}`;
		await page.getByLabel("Name").fill(name);
		// The task field is a mention editor (contenteditable), not a textarea.
		const task = page.locator('[contenteditable="true"]').first();
		await task.click();
		await task.pressSequentially("Summarise this week's new sources.");
		await expect(page.getByText("Summarise this week's new sources.").last()).toBeVisible();
		await page.getByRole("tab", { name: "Edit as JSON" }).click();
		// The JSON is the create request as sent: nothing names an account.
		await expect(page.getByText(/workspace_id/)).toHaveCount(0);
		await expectNoOverflow(page, "automation JSON editor");
		await page.getByRole("tab", { name: "Form" }).click();
		await expectNoOverflow(page, "new automation");
		// Nothing gates creation on a model: a run uses the account's chat
		// model, and the form says which that is. (The Python tier wanted an
		// image and a vision model chosen first, which this stack never had,
		// so there the button stayed disabled.)
		await expect(page.getByRole("combobox", { name: "Runs on" })).toContainText("Your chat model");
		const create = page.getByRole("button", { name: "Create automation" }).first();
		await expect(create).toBeEnabled();
		await create.click();
		await expect(page).toHaveURL(/\/dashboard\/automations\/\d+$/);
		await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
		await expectNoOverflow(page, "automation created from the form");

		// The account is every test's: take the automation away again.
		const id = Number(new URL(page.url()).pathname.split("/").pop());
		const removed = await request.delete(`/api/v1/automations/${id}`, {
			headers: authHeaders(token),
		});
		expect(removed.ok(), await removed.text()).toBeTruthy();
	});

	test("detail: pause/resume, edit, delete (automation created through the API)", async ({
		page,
		request,
		token,
	}) => {
		const name = `E2E weekly digest ${Date.now().toString(36)}`;
		const res = await request.post("/api/v1/automations", {
			headers: authHeaders(token),
			data: {
				name,
				definition: {
					name,
					plan: [
						{ step_id: "s1", action: "agent_task", params: { query: "Summarise this week." } },
					],
				},
			},
		});
		expect(res.ok(), await res.text()).toBeTruthy();
		const id = ((await res.json()) as { id: number }).id;

		await page.goto("/dashboard/settings/automations");
		await expect(page.getByRole("link", { name })).toBeVisible();
		await expectNoOverflow(page, "automations list");
		await page.getByRole("link", { name }).click();

		await expect(page).toHaveURL(new RegExp(`/dashboard/automations/${id}$`));
		await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
		await expectNoOverflow(page, "automation detail");

		// Every control is the owner's: no permission hides any of them.
		await page.getByRole("button", { name: "Pause" }).click();
		await expect(page.getByRole("button", { name: "Resume" })).toBeVisible();
		await page.getByRole("button", { name: "Resume" }).click();
		await expect(page.getByRole("button", { name: "Pause" })).toBeVisible();

		await page.getByRole("link", { name: "Edit" }).click();
		await expect(page).toHaveURL(new RegExp(`/dashboard/automations/${id}/edit$`));
		await expect(page.getByRole("heading", { name: "Edit automation", level: 1 })).toBeVisible();
		await expectNoOverflow(page, "edit automation");
		await page.getByLabel("Name").fill(`${name} v2`);
		await page.getByRole("button", { name: "Save changes" }).first().click();
		await expect(page.getByRole("heading", { name: `${name} v2`, level: 1 })).toBeVisible();

		await page.getByRole("button", { name: "Delete" }).click();
		const alert = page.getByRole("alertdialog", { name: "Delete this automation?" });
		await expect(alert).toBeVisible();
		await expectNoOverflow(page, "delete automation dialog");
		await alert.getByRole("button", { name: "Delete" }).click();
		await expect(page).toHaveURL(/\/dashboard\/settings\/automations$/);
		await expect
			.poll(async () =>
				(await request.get(`/api/v1/automations/${id}`, { headers: authHeaders(token) })).status()
			)
			.toBe(404);
	});
});

test.describe("Logs", () => {
	test("task logs page renders its summary and filters", async ({ page }) => {
		await page.goto("/dashboard/logs");
		await expect(page.getByRole("heading", { name: "Task Logs" })).toBeVisible();
		for (const card of ["Total Logs", "Active Tasks", "Success Rate", "Recent Failures"]) {
			await expect(page.getByText(card).first()).toBeVisible();
		}
		await page.getByPlaceholder("Filter by message...").fill("nothing-matches");
		await expectNoOverflow(page, "logs page");
		await page.getByRole("button", { name: "Refresh" }).click();
	});
});
