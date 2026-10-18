import { expect, type Locator, type Page, type TestInfo } from "@playwright/test";

/**
 * Phone layout checks, run after opening every page, dialog and sheet.
 *
 * - No horizontal document scroll: scrollWidth <= clientWidth.
 * - No visible element sticks out past the viewport's left or right edge.
 *   Content inside something that scrolls sideways on purpose (overflow-x
 *   auto/scroll: tab strips, tables, code) is allowed to, and so is content
 *   clipped by an overflow-hidden ancestor that itself sits inside the
 *   viewport. Elements entirely off screen (closed drawers, sr-only text)
 *   are not visible and are ignored.
 * - Text inputs are at least 16px, so iOS Safari does not zoom on focus.
 *
 * On desktop the same checks run (cheap, and a 1280px overflow is a bug too);
 * the 16px input rule is phone-only.
 */

export type Offender = {
	selector: string;
	left: number;
	right: number;
	width: number;
	text: string;
};

export type LayoutReport = {
	scrollWidth: number;
	clientWidth: number;
	offenders: Offender[];
	smallInputs: { selector: string; fontSize: number }[];
};

export async function collectLayoutReport(page: Page): Promise<LayoutReport> {
	return page.evaluate(() => {
		const vw = document.documentElement.clientWidth;
		const tolerance = 1;

		const describe = (el: Element): string => {
			const parts: string[] = [];
			let node: Element | null = el;
			for (let i = 0; node && i < 4; i++) {
				let part = node.tagName.toLowerCase();
				const testid = node.getAttribute("data-testid");
				const label = node.getAttribute("aria-label");
				const role = node.getAttribute("role");
				if (testid) part += `[data-testid="${testid}"]`;
				else if (label) part += `[aria-label="${label.slice(0, 40)}"]`;
				else if (role) part += `[role="${role}"]`;
				else if (node.classList.length) {
					part += `.${Array.from(node.classList).slice(0, 3).join(".")}`;
				}
				parts.unshift(part);
				node = node.parentElement;
			}
			return parts.join(" > ");
		};

		const isVisible = (style: CSSStyleDeclaration): boolean => {
			if (style.visibility === "hidden" || style.display === "none") return false;
			if (Number.parseFloat(style.opacity) === 0) return false;
			return true;
		};

		const offenders: Offender[] = [];
		const offenderEls: Element[] = [];
		const all = document.body.querySelectorAll("*");
		for (const el of Array.from(all)) {
			if (el.closest("[data-overflow-check-ignore]")) continue;
			// Toasts slide in from the edge; mid-animation they are always
			// partly off screen, which says nothing about the layout.
			if (el.closest("[data-sonner-toaster]")) continue;
			const style = getComputedStyle(el);
			if (!isVisible(style)) continue;
			const rect = el.getBoundingClientRect();
			if (rect.width <= 1 || rect.height <= 1) continue;
			// Entirely off screen: not visible, not an overflow.
			if (rect.right <= 0 || rect.left >= vw) continue;

			let left = rect.left;
			let right = rect.right;
			let scrollsOnPurpose = false;
			let hidden = false;
			for (let a = el.parentElement; a && a !== document.documentElement; a = a.parentElement) {
				const s = getComputedStyle(a);
				if (s.display === "none" || s.visibility === "hidden") {
					hidden = true;
					break;
				}
				const ox = s.overflowX;
				if (ox === "auto" || ox === "scroll") {
					scrollsOnPurpose = true;
					break;
				}
				if (ox === "hidden" || ox === "clip") {
					const r = a.getBoundingClientRect();
					left = Math.max(left, r.left);
					right = Math.min(right, r.right);
				}
			}
			if (hidden || scrollsOnPurpose) continue;
			if (right - left <= 1) continue;
			if (left < -tolerance || right > vw + tolerance) {
				// A too-wide container drags every child with it; report the
				// outermost element only, one line per root cause.
				if (offenderEls.some((o) => o.contains(el))) continue;
				offenderEls.push(el);
				offenders.push({
					selector: describe(el),
					left: Math.round(left),
					right: Math.round(right),
					width: Math.round(rect.width),
					text: (el.textContent ?? "").trim().slice(0, 60),
				});
			}
		}

		const smallInputs: { selector: string; fontSize: number }[] = [];
		const inputs = document.querySelectorAll(
			'input:not([type]), input[type="text"], input[type="search"], input[type="email"], input[type="url"], input[type="password"], input[type="number"], input[type="tel"], textarea, [contenteditable="true"]'
		);
		for (const el of Array.from(inputs)) {
			const style = getComputedStyle(el);
			if (!isVisible(style)) continue;
			const rect = el.getBoundingClientRect();
			if (rect.width <= 1 || rect.height <= 1) continue;
			const size = Number.parseFloat(style.fontSize);
			if (size < 16) smallInputs.push({ selector: describe(el), fontSize: size });
		}

		return {
			scrollWidth: document.documentElement.scrollWidth,
			clientWidth: vw,
			offenders: offenders.slice(0, 15),
			smallInputs,
		};
	});
}

function isPhone(page: Page, testInfo?: TestInfo): boolean {
	const width = page.viewportSize()?.width ?? 1280;
	return width < 640 || testInfo?.project.name.includes("phone") === true;
}

/**
 * Assert the page (or the dialog now open on it) fits the viewport.
 * `label` names the screen in the failure message.
 */
export async function expectNoOverflow(page: Page, label: string): Promise<void> {
	// Let enter animations (dialogs, sheets: ~200ms) settle first.
	await page.waitForTimeout(350);
	const report = await collectLayoutReport(page);
	expect
		.soft(
			report.scrollWidth,
			`${label}: document scrolls sideways (scrollWidth ${report.scrollWidth} > clientWidth ${report.clientWidth})`
		)
		.toBeLessThanOrEqual(report.clientWidth);
	expect
		.soft(
			report.offenders,
			`${label}: elements extend past the ${report.clientWidth}px viewport:\n${JSON.stringify(report.offenders, null, 2)}`
		)
		.toEqual([]);
	if (isPhone(page)) {
		expect
			.soft(
				report.smallInputs,
				`${label}: text inputs under 16px zoom the page on iOS:\n${JSON.stringify(report.smallInputs, null, 2)}`
			)
			.toEqual([]);
	}
}

/** Open something (a dialog, a sheet, a menu), then check the layout. */
export async function openAndCheck(
	page: Page,
	label: string,
	open: () => Promise<unknown>,
	visible?: Locator
): Promise<void> {
	await open();
	if (visible) await expect(visible).toBeVisible();
	await expectNoOverflow(page, label);
}

/**
 * The header sits below the top safe area: its CSS pads it by
 * env(safe-area-inset-top). Chromium cannot emulate a notch, so the check is
 * that a rule applying to the header uses the inset.
 */
export async function expectHeaderClearsSafeArea(header: Locator): Promise<void> {
	const usesInset = await header.evaluate((el) => {
		const matches = (rule: CSSRule): boolean => {
			if (rule instanceof CSSStyleRule) {
				try {
					return el.matches(rule.selectorText) && /safe-area-inset-top/.test(rule.style.cssText);
				} catch {
					return false;
				}
			}
			if ("cssRules" in rule) {
				return Array.from((rule as CSSGroupingRule).cssRules).some(matches);
			}
			return false;
		};
		for (const sheet of Array.from(document.styleSheets)) {
			let rules: CSSRuleList;
			try {
				rules = sheet.cssRules;
			} catch {
				continue;
			}
			if (Array.from(rules).some(matches)) return true;
		}
		return /safe-area-inset-top/.test(el.getAttribute("style") ?? "");
	});
	expect(usesInset, "notebook header is padded by env(safe-area-inset-top)").toBe(true);
	const box = await header.boundingBox();
	expect(box?.y ?? -1, "header starts at the top of the viewport").toBeGreaterThanOrEqual(0);
}
