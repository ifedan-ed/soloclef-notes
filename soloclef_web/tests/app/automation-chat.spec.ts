import type { Locator, Page } from "@playwright/test";
import { authHeaders, BACKEND_URL } from "../helpers/api/auth";
import { assistantMessages, composer, send } from "../helpers/ui/chat";
import { expect, test, uniqueName, waitForChatReady } from "./fixtures";

/**
 * Automations from the chat, end to end on the real stack with the stand-in
 * model (docker/e2e-web-go/model-stub, markers E2E_AUTOMATION_*):
 *
 * - "Create via chat" on the Automations page lists the person's notebooks
 *   and opens the chosen one with the request started in its chat box;
 * - the model drafts an automation through the real create_automation tool,
 *   which checks it with the create route's own code and keeps it as a
 *   draft; the card shows it, the person edits it there, Create saves it
 *   through the ordinary create route, and it is in the list with the
 *   schedule as edited, in the device's time zone;
 * - a vague request gets a question and no card, and an impossible one is
 *   answered with what automations cannot do: the stub gives either only
 *   when the product's own tool description and prompt say so.
 *
 * Every test signs in as the same account, so each finds its automation by
 * a run-unique name.
 */

const SHOTS = process.env.E2E_SCREENSHOT_DIR;
// The device's zone, which the chat request carries and the draft uses.
const ZONE = "Africa/Lagos";
test.use({ timezoneId: ZONE });

/**
 * The whole card, in a viewport tall enough to hold it: the chat scrolls
 * inside its own pane, so a card taller than the screen is cut off there.
 */
async function shootWhole(page: Page, card: Locator, path: string) {
	const size = page.viewportSize();
	if (!size) return;
	await page.setViewportSize({ width: size.width, height: 2000 });
	await card.getByText("More settings").click();
	await card.scrollIntoViewIfNeeded();
	await card.screenshot({ path });
	await card.getByText("More settings").click();
	await page.setViewportSize(size);
}

type Detail = {
	id: number;
	name: string;
	triggers: { type: string; params: { cron: string; timezone: string } }[];
};

test("Create via chat drafts an automation the card edits and creates", async ({
	page,
	request,
	token,
	notebook,
}, testInfo) => {
	test.setTimeout(120_000);
	const project = testInfo.project.name;
	const drafted = uniqueName("E2E digest");
	const edited = `${drafted} edited`;

	// The entry point: the person's notebooks, newest first; theirs is the newest.
	await page.goto("/dashboard/automations");
	await page.getByTestId("create-via-chat").first().click();
	const item = page.getByRole("menuitem", { name: notebook.title });
	await expect(item).toBeVisible();
	if (SHOTS) await page.screenshot({ path: `${SHOTS}/create-via-chat-menu-${project}.png` });
	await item.click();
	await expect(page).toHaveURL(new RegExp(`/dashboard/notebooks/${notebook.id}$`));
	await waitForChatReady(page);
	// The request is started in the box, not sent.
	await expect(composer(page)).toHaveText("Create an automation that ");
	if (SHOTS) await page.screenshot({ path: `${SHOTS}/create-via-chat-composer-${project}.png` });

	// The person finishes the sentence after the started words.
	const box = composer(page);
	await box.click();
	await box.press("End");
	await box.pressSequentially(`E2E_AUTOMATION_DRAFT ${drafted}`, { delay: 20 });
	await page.getByRole("button", { name: "Send message" }).click();
	const card = page.getByTestId("automation-draft-card");
	await expect(card.getByTestId("automation-draft-title")).toHaveText("Automation draft", {
		timeout: 30_000,
	});
	await expect(assistantMessages(page).last()).toContainText("on the card for you to check");
	await expect(card.getByLabel("Name")).toHaveValue(drafted);
	await expect(card.getByText(ZONE).first()).toBeVisible();
	if (SHOTS) await shootWhole(page, card, `${SHOTS}/automation-draft-card-${project}.png`);

	// Edit it on the card: a new name, and 09:30 instead of the drafted 08:00.
	await card.getByLabel("Name").fill(edited);
	await card.locator('input[type="time"]').fill("09:30");
	await card.getByTestId("automation-draft-create").click();
	await expect(card.getByTestId("automation-draft-title")).toHaveText("Automation created");
	await expect(card.getByTestId("automation-draft-open")).toBeVisible();
	// It shows what was saved, the edits included, not the draft as drafted.
	await expect(card.getByText(edited)).toBeVisible();
	await expect(card.getByText(`Mon at 09:30 (${ZONE})`)).toBeVisible();
	if (SHOTS) await card.screenshot({ path: `${SHOTS}/automation-draft-created-${project}.png` });

	// It is in the list, with the schedule as edited, in the device's zone.
	await page.goto("/dashboard/automations");
	await expect(page.getByText(edited)).toBeVisible();
	const list = await request.get(`${BACKEND_URL}/api/v1/automations?limit=200`, {
		headers: authHeaders(token),
	});
	const mine = ((await list.json()) as { items: { id: number; name: string }[] }).items.find(
		(a) => a.name === edited
	);
	expect(mine, "the created automation is listed").toBeTruthy();
	const detail = (await (
		await request.get(`${BACKEND_URL}/api/v1/automations/${mine?.id}`, {
			headers: authHeaders(token),
		})
	).json()) as Detail;
	expect(detail.triggers).toHaveLength(1);
	expect(detail.triggers[0].type).toBe("schedule");
	expect(detail.triggers[0].params).toEqual({ cron: "30 9 * * 1", timezone: ZONE });

	// A reload shows the card as created: Create cannot run twice.
	await page.goto(`/dashboard/notebooks/${notebook.id}`);
	await expect(page.getByTestId("automation-draft-title")).toHaveText("Automation created", {
		timeout: 30_000,
	});
	await request.delete(`${BACKEND_URL}/api/v1/automations/${mine?.id}`, {
		headers: authHeaders(token),
	});
});

test("a vague request gets a question, and an impossible one is told what cannot be done", async ({
	page,
	notebook,
}, testInfo) => {
	test.setTimeout(90_000);
	await page.goto(`/dashboard/notebooks/${notebook.id}`);
	const width = page.viewportSize()?.width ?? 1280;
	if (width < 1024) await page.getByRole("tab", { name: /^Chat/ }).click();
	await waitForChatReady(page);

	await send(page, "Remind me about papers sometimes. E2E_AUTOMATION_VAGUE");
	await expect(assistantMessages(page).last()).toContainText("which day and time should it run", {
		timeout: 30_000,
	});
	await expect(page.getByTestId("automation-draft-card")).toHaveCount(0);
	if (SHOTS)
		await page.screenshot({ path: `${SHOTS}/automation-ask-${testInfo.project.name}.png` });

	await send(page, "Every 30 seconds, email my colleague the news. E2E_AUTOMATION_CANNOT");
	const answer = assistantMessages(page).last();
	// The product's own sentences, from soloclef_automations' lists.
	await expect(answer).toContainText("Run more often than once a minute", { timeout: 30_000 });
	await expect(answer).toContainText("Send email to anyone");
	await expect(page.getByTestId("automation-draft-card")).toHaveCount(0);
	if (SHOTS)
		await page.screenshot({
			path: `${SHOTS}/automation-cannot-${testInfo.project.name}.png`,
		});
});
