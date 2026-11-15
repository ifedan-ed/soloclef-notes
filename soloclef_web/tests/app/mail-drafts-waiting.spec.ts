import type { APIRequestContext, Page } from "@playwright/test";
import { BACKEND_URL } from "../helpers/api/auth";
import { expect, test, uniqueName } from "./fixtures";

/**
 * Drafts waiting: a draft whose chat is gone (an automation's run deletes
 * its conversation when it ends) is opened from its inbox row, drawn on the
 * same card the chat shows, with its single-use confirmation and the
 * unusual-send point to tick, and discarded from there, leaving the list.
 *
 * The draft is seeded (/__e2e__/seed/mail-draft) in the send guard's own
 * shape, because the stack has no mail server for a model to draft
 * through. Its mailbox is not one anybody runs, so the card's check of
 * whether the recipient was mailed before cannot reach it: that is the
 * unusual point the card asks to be ticked, which the screenshots show.
 * Nothing here presses Send; the send itself is the guard's, tested
 * against a real Postgres and a real Stalwart in soloclef_assistant.
 *
 * Every test signs in as the same account, so each finds its own draft by
 * a run-unique subject and never assumes the list holds only it.
 */

const SHOTS = process.env.E2E_SCREENSHOT_DIR;
const EMAIL = process.env.PLAYWRIGHT_TEST_EMAIL ?? "e2e-test@soloclef.net";
const SECRET = process.env.E2E_MINT_SECRET ?? "e2e-mint-secret-not-for-production";
const PAGE = "/dashboard/mail/drafts";

function seedHeaders() {
	// The request context carries the session cookie, so the backend's CSRF
	// check wants a same-site Origin.
	return { "X-E2E-Mint-Secret": SECRET, Origin: process.env.PLAYWRIGHT_BASE_URL ?? BACKEND_URL };
}

/** A waiting draft, and the inbox row the server raises for one. */
async function seedWaitingDraft(request: APIRequestContext, subject: string, rowTitle: string) {
	const draft = await request.post(`${BACKEND_URL}/__e2e__/seed/mail-draft`, {
		data: {
			email: EMAIL,
			subject,
			body: "Hello Ben,\n\nThe invoice is paid. The receipt is attached to the ledger.\n\nAda",
			to: ["ben@example.test"],
			mailbox: "Work",
		},
		headers: seedHeaders(),
	});
	expect(draft.status(), await draft.text()).toBe(201);
	const { draft_id: draftId } = (await draft.json()) as { draft_id: string };
	const row = await request.post(`${BACKEND_URL}/__e2e__/notifications`, {
		data: {
			email: EMAIL,
			notifications: [
				{
					type: "mail_draft",
					title: rowTitle,
					message: "Confirm it to send it, or discard it. Nothing is sent until you do.",
					// What soloclef_notify writes for a waiting draft
					// (maildraftnotice.WaitingPage is the link).
					metadata: { status: "completed", draft_id: draftId, account_label: "Work", link: PAGE },
				},
			],
		},
		headers: seedHeaders(),
	});
	expect(row.status(), await row.text()).toBe(201);
	return draftId;
}

async function openRowFromInbox(page: Page, isPhone: boolean, rowTitle: string) {
	await page.goto("/dashboard/notebooks");
	if (isPhone) await page.getByRole("button", { name: "Open menu" }).click();
	await page.getByRole("button", { name: "Notifications", exact: true }).click();
	await page.getByText(rowTitle).click();
}

test("a waiting draft opens from its inbox row and is discarded from the page", async ({
	page,
	request,
	isPhone,
}, testInfo) => {
	const subject = uniqueName("Invoice paid");
	const rowTitle = uniqueName("A mail draft is waiting for you");
	await seedWaitingDraft(request, subject, rowTitle);

	await openRowFromInbox(page, isPhone, rowTitle);
	await expect(page).toHaveURL(new RegExp(`${PAGE}$`));
	await expect(page.getByRole("heading", { name: "Drafts waiting" })).toBeVisible();

	const item = page.getByTestId("drafts-waiting-item").filter({ hasText: subject });
	await expect(item).toBeVisible();
	await expect(item.getByText("From Work")).toBeVisible();
	const card = item.getByTestId("mail-draft-card");
	// The same card as in the chat: the stored message and its go-ahead.
	await expect(card.getByText("Email draft")).toBeVisible();
	await expect(card.getByText("ben@example.test").first()).toBeVisible();
	const send = card.getByTestId("mail-draft-send");
	await expect(send).toBeVisible();
	// The recipients could not be checked, so Send waits for the tick.
	await expect(send).toBeDisabled();
	await expect(card.getByRole("checkbox").first()).toBeVisible();

	if (SHOTS) {
		await item.scrollIntoViewIfNeeded();
		await page.screenshot({
			path: `${SHOTS}/drafts-waiting-${testInfo.project.name}.png`,
			fullPage: true,
		});
	}

	// Ticking the point is what lets Send be pressed; it is not pressed here.
	await card.getByRole("checkbox").first().check();
	await expect(send).toBeEnabled();

	await card.getByTestId("mail-draft-discard").click();
	await expect(page.getByText("Draft discarded")).toBeVisible();
	await expect(item).toHaveCount(0);

	// Read again from the server, it is gone from the list for good.
	await page.reload();
	await expect(page.getByRole("heading", { name: "Drafts waiting" })).toBeVisible();
	await expect(page.getByTestId("drafts-waiting-item").filter({ hasText: subject })).toHaveCount(0);
});
