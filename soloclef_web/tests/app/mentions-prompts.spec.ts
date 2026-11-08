import { authHeaders } from "../helpers/api/auth";
import { addNoteSource } from "../helpers/api/notebooks";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { composer } from "../helpers/ui/chat";
import { expect, openNotebook, test } from "./fixtures";

/** "@" mentions list only this notebook's sources; "/" lists saved prompts. */

test.describe("Composer @ mentions", () => {
	test("an empty notebook has nothing to mention", async ({ page, workspace, notebook }) => {
		await openNotebook(page, workspace.id, notebook.id, "Chat");
		await composer(page).click();
		await composer(page).pressSequentially("@");
		await expect(page.getByText("This notebook has no sources to mention yet.")).toBeVisible();
		await expectNoOverflow(page, "@ picker (empty)");
	});

	test("@ lists only notebook sources, and the pick is sent with the message", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		const mine = await addNoteSource(
			request,
			token,
			workspace.id,
			notebook.id,
			"Enzyme kinetics",
			"Km and Vmax."
		);
		// A document in the same workspace but a different notebook must not be offered.
		const other = await request.post(`/api/v1/workspaces/${workspace.id}/notebooks`, {
			headers: authHeaders(token),
			data: { title: "Other" },
		});
		const otherId = ((await other.json()) as { id: number }).id;
		await addNoteSource(request, token, workspace.id, otherId, "Unrelated geology", "Rocks.");

		await openNotebook(page, workspace.id, notebook.id, "Chat");
		await composer(page).click();
		await composer(page).pressSequentially("Explain @");
		const list = page.getByRole("listbox").filter({ hasText: "Sources in this notebook" });
		await expect(list).toBeVisible();
		await expect(list.getByRole("button", { name: /Enzyme kinetics/ })).toBeVisible();
		await expect(list.getByText("Unrelated geology")).toHaveCount(0);
		await expectNoOverflow(page, "@ picker");

		await composer(page).pressSequentially("zzz");
		await expect(page.getByText("No source matches")).toBeVisible();
		for (let i = 0; i < 3; i++) await composer(page).press("Backspace");

		await list.getByRole("button", { name: /Enzyme kinetics/ }).click();
		await expect(
			page.getByRole("button", { name: "Remove mention Enzyme kinetics" })
		).toBeVisible();

		const sent = page.waitForRequest(
			(r) => r.url().endsWith("/api/v1/new_chat") && r.method() === "POST"
		);
		await composer(page).pressSequentially(" E2E_ANSWER");
		await page.getByRole("button", { name: "Send message" }).click();
		const body = (await sent).postDataJSON() as { mentioned_document_ids?: number[] };
		expect(body.mentioned_document_ids).toEqual([mine.target_id]);
	});
});

test.describe("Composer / saved prompts", () => {
	test("/ lists saved prompts; picking one fills the composer", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		const name = `E2E prompt ${Date.now()}`;
		const created = await request.post("/api/v1/prompts", {
			headers: authHeaders(token),
			data: { name, prompt: "Summarise the key findings in three bullets.", mode: "transform" },
		});
		expect(created.ok()).toBeTruthy();
		const promptId = ((await created.json()) as { id: number }).id;
		try {
			await openNotebook(page, workspace.id, notebook.id, "Chat");
			await composer(page).click();
			await composer(page).pressSequentially("/");
			await expect(page.getByText("Saved prompts")).toBeVisible();
			await expect(page.getByRole("button", { name: "Create a prompt" })).toBeVisible();
			await expectNoOverflow(page, "/ prompt picker");
			await page.getByRole("button", { name }).click();
			await expect(composer(page)).toContainText("Summarise the key findings in three bullets.");
		} finally {
			await request.delete(`/api/v1/prompts/${promptId}`, { headers: authHeaders(token) });
		}
	});
});

test.describe("Composer: sources only", () => {
	test("no Sources / + Web switch on desktop or in the phone + menu; the Tools menu stays", async ({
		page,
		workspace,
		notebook,
		isPhone,
	}) => {
		// A notebook chat answers from its sources only, so the composer offers
		// no search scope. The Tools menu (one web tool for one message) stays.
		await openNotebook(page, workspace.id, notebook.id, "Chat");
		const root = page.locator(".aui-composer-root");
		await expect(
			root.getByRole("button", { name: "Pick a tool for the next message" })
		).toBeVisible();
		// The old switch: "Sources" / "+ Web" segments labelled "Search scope".
		await expect(root.getByLabel(/Search scope/i)).toHaveCount(0);
		await expect(root.getByLabel(/Answer from this notebook's sources/)).toHaveCount(0);
		await expect(root.getByText(/^\+ Web$|Sources \+ Web/)).toHaveCount(0);

		if (isPhone) {
			await root.getByRole("button", { name: "Upload files, manage tools and more" }).click();
			const drawer = page.getByRole("dialog");
			await expect(drawer.getByRole("button", { name: /Upload Files/ })).toBeVisible();
			await expect(drawer.getByRole("button", { name: /Manage Tools/ })).toBeVisible();
			await expect(drawer.getByText(/Search Scope/i)).toHaveCount(0);
			await expectNoOverflow(page, "phone + menu");
			await page.keyboard.press("Escape");
			await expect(drawer).toBeHidden();
		}
	});
});
