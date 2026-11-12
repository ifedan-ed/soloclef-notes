import { assistantMessages, send } from "../helpers/ui/chat";
import { expect, openNotebook, test } from "./fixtures";

/**
 * Selecting text in a long chat by dragging down must scroll the chat.
 *
 * A browser scrolls a scrolling area while a selection is dragged only when
 * the pointer leaves that area. The composer used to sit inside the chat's
 * scrolling area, pinned over its bottom, so dragging a selection down onto
 * it scrolled nothing, and nothing below it could be selected (reported
 * 2026-11-06: "you cant scroll down to select more"). The composer now sits
 * below the scrolling area (components/assistant-ui/chat-viewport.tsx).
 */
test.describe("Chat selection", () => {
	test("dragging a selection down onto the composer scrolls the chat", async ({
		page,
		notebook,
		isPhone,
	}) => {
		test.skip(isPhone, "drag-selecting with a mouse is a desktop gesture");
		await openNotebook(page, notebook.id, "Chat");
		const viewport = page.locator(".aui-thread-viewport");

		// A conversation well past one screen.
		for (let i = 1; i <= 12; i++) {
			await send(page, `E2E_ANSWER question ${i}`);
			await expect(assistantMessages(page)).toHaveCount(i, { timeout: 45_000 });
			await expect(assistantMessages(page).last()).toContainText("E2E notebook answer", {
				timeout: 45_000,
			});
			const overflow = await viewport.evaluate((el) => el.scrollHeight - el.clientHeight);
			if (overflow > 900) break;
		}
		await expect
			.poll(() => viewport.evaluate((el) => el.scrollHeight - el.clientHeight))
			.toBeGreaterThan(600);

		// Back to the top, as when reading a long answer from its start.
		await viewport.evaluate((el) => {
			el.style.scrollBehavior = "auto";
			el.scrollTop = 0;
		});
		// The last question that shows before any scrolling.
		const lastVisible = await page.evaluate(() => {
			const vp = document.querySelector(".aui-thread-viewport");
			const composer = document.querySelector(".aui-composer-root");
			if (!vp || !composer) return 0;
			const bottom = Math.min(
				vp.getBoundingClientRect().bottom,
				composer.getBoundingClientRect().top
			);
			let n = 0;
			for (const el of document.querySelectorAll('[data-role="user"]')) {
				if (el.getBoundingClientRect().top < bottom) n++;
			}
			return n;
		});
		const first = page.locator('[data-role="user"]').first();
		const start = await first.boundingBox();
		const composerBox = await page.locator(".aui-composer-root").boundingBox();
		if (!start || !composerBox) throw new Error("chat not laid out");

		// Press on the first message's text and drag down onto the composer, then hold.
		await page.mouse.move(start.x + 20, start.y + start.height / 2);
		await page.mouse.down();
		const target = composerBox.y + composerBox.height / 2;
		for (let y = start.y + start.height / 2; y < target; y += 25) {
			await page.mouse.move(start.x + 200, y);
		}
		await page.mouse.move(start.x + 200, target);
		await page.waitForTimeout(2000);
		const scrolled = await viewport.evaluate((el) => el.scrollTop);
		await page.mouse.up();

		// The chat scrolled on, and the selection reaches past the first screen.
		expect(scrolled).toBeGreaterThan(300);
		const selected = await page.evaluate(() => String(window.getSelection()));
		expect(selected).toContain(`question ${lastVisible + 1}`);
	});
});
