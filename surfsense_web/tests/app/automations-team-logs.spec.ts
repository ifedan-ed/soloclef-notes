import { authHeaders } from "../helpers/api/auth";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, test } from "./fixtures";

/** Automations (list, new, detail, edit), Team, and Logs. */

test.describe("Automations", () => {
	test("empty list and the builder form (creation gated on image/vision models)", async ({
		page,
		workspace,
	}) => {
		await page.goto(`/dashboard/${workspace.id}/automations`);
		await expect(page).toHaveURL(/\/workspace-settings\/automations$/);
		await expect(page.getByRole("heading", { name: "No automations yet" })).toBeVisible();
		await expectNoOverflow(page, "automations (empty)");

		await page.getByRole("link", { name: "Create manually" }).first().click();
		await expect(page.getByRole("heading", { name: "New automation", level: 1 })).toBeVisible();
		await page.getByLabel("Name").fill("E2E weekly digest");
		// The task field is a mention editor (contenteditable), not a textarea.
		const task = page.locator('[contenteditable="true"]').first();
		await task.click();
		await task.pressSequentially("Summarise this week's new sources.");
		await expect(page.getByText("Summarise this week's new sources.").last()).toBeVisible();
		await page.getByRole("tab", { name: "Edit as JSON" }).click();
		await expectNoOverflow(page, "automation JSON editor");
		await page.getByRole("tab", { name: "Form" }).click();
		await expectNoOverflow(page, "new automation");
		// The isolated stack has no image or vision model, and an automation
		// needs a model for every slot, so Create stays disabled.
		await expect(page.getByText("No models available").first()).toBeVisible();
		await expect(page.getByRole("button", { name: "Create automation" }).first()).toHaveAttribute(
			"aria-disabled",
			"true"
		);
	});

	test("detail: pause/resume, edit, delete (automation created through the API)", async ({
		page,
		workspace,
		request,
		token,
	}) => {
		const res = await request.post("/api/v1/automations", {
			headers: authHeaders(token),
			data: {
				workspace_id: workspace.id,
				name: "E2E weekly digest",
				definition: {
					name: "E2E weekly digest",
					plan: [
						{ step_id: "s1", action: "agent_task", params: { query: "Summarise this week." } },
					],
				},
			},
		});
		expect(res.ok(), await res.text()).toBeTruthy();
		const id = ((await res.json()) as { id: number }).id;

		await page.goto(`/dashboard/${workspace.id}/workspace-settings/automations`);
		await expect(page.getByRole("link", { name: "E2E weekly digest" })).toBeVisible();
		await expectNoOverflow(page, "automations list");
		await page.getByRole("link", { name: "E2E weekly digest" }).click();

		await expect(page).toHaveURL(new RegExp(`/automations/${id}$`));
		await expect(page.getByRole("heading", { name: "E2E weekly digest", level: 1 })).toBeVisible();
		await expectNoOverflow(page, "automation detail");

		await page.getByRole("button", { name: "Pause" }).click();
		await expect(page.getByRole("button", { name: "Resume" })).toBeVisible();
		await page.getByRole("button", { name: "Resume" }).click();
		await expect(page.getByRole("button", { name: "Pause" })).toBeVisible();

		await page.getByRole("link", { name: "Edit" }).click();
		await expect(page.getByRole("heading", { name: "Edit automation", level: 1 })).toBeVisible();
		await expectNoOverflow(page, "edit automation");
		await page.getByLabel("Name").fill("E2E weekly digest v2");
		await page.getByRole("button", { name: "Save changes" }).first().click();
		await expect(
			page.getByRole("heading", { name: "E2E weekly digest v2", level: 1 })
		).toBeVisible();

		await page.getByRole("button", { name: "Delete" }).click();
		const alert = page.getByRole("alertdialog", { name: "Delete this automation?" });
		await expect(alert).toBeVisible();
		await expectNoOverflow(page, "delete automation dialog");
		await alert.getByRole("button", { name: "Delete" }).click();
		await expect
			.poll(async () =>
				(await request.get(`/api/v1/automations/${id}`, { headers: authHeaders(token) })).status()
			)
			.toBe(404);
	});
});

test.describe("Team", () => {
	test("members table, invite dialog, pending invitations", async ({ page, workspace }) => {
		await page.goto(`/dashboard/${workspace.id}/team`);
		await expect(page.getByRole("heading", { name: "Members", level: 1 })).toBeVisible();
		await expect(page.getByRole("table")).toBeVisible();
		await expect(page.getByRole("heading", { name: "Pending invitations" })).toBeVisible();
		await expect(page.getByText("No pending invitations.")).toBeVisible();
		await expectNoOverflow(page, "team page");

		await page.getByRole("button", { name: "Invite", exact: true }).click();
		const dialog = page.getByRole("dialog", { name: "Invite someone" });
		await expect(dialog).toBeVisible();
		await expectNoOverflow(page, "invite dialog");
		// Roles on offer never include Owner.
		await dialog.getByLabel("Role").click();
		await expect(page.getByRole("option", { name: "Editor" })).toBeVisible();
		await expect(page.getByRole("option", { name: "Viewer" })).toBeVisible();
		await expect(page.getByRole("option", { name: "Owner" })).toHaveCount(0);
		await page.keyboard.press("Escape");
		// A malformed address is caught before anything is sent.
		await dialog.getByLabel("Email address").fill("not-an-address");
		await dialog.getByRole("button", { name: "Send invitation" }).click();
		await expect(dialog.getByRole("alert")).toContainText("valid email");
		await dialog.getByRole("button", { name: "Cancel" }).click();
		await expect(dialog).toBeHidden();
	});
});

test.describe("Logs", () => {
	test("task logs page renders its summary and filters", async ({ page, workspace }) => {
		await page.goto(`/dashboard/${workspace.id}/logs`);
		await expect(page.getByRole("heading", { name: "Task Logs" })).toBeVisible();
		for (const card of ["Total Logs", "Active Tasks", "Success Rate", "Recent Failures"]) {
			await expect(page.getByText(card).first()).toBeVisible();
		}
		await page.getByPlaceholder("Filter by message...").fill("nothing-matches");
		await expectNoOverflow(page, "logs page");
		await page.getByRole("button", { name: "Refresh" }).click();
	});
});
