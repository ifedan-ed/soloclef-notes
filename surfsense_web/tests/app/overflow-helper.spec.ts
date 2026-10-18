import { collectLayoutReport } from "../helpers/layout/overflow";
import { expect, test } from "./fixtures";

/**
 * The overflow checker itself: it must catch what it claims to catch, or every
 * green layout assertion in the suite means nothing.
 */
test.describe("overflow checker", () => {
	test("flags a too-wide element, spares scrollers and clipped content", async ({ page }) => {
		await page.setContent(`
			<meta name="viewport" content="width=device-width, initial-scale=1">
			<body style="margin:0">
				<div id="ok" style="width:100px;height:20px"></div>
				<div id="wide" style="width:2000px;height:20px;background:red"></div>
				<div style="overflow-x:auto;width:100%"><div id="scroller-child" style="width:3000px;height:20px"></div></div>
				<div style="overflow:hidden;width:100px"><div id="clipped" style="width:3000px;height:20px"></div></div>
				<div id="offscreen" style="position:fixed;left:5000px;top:0;width:50px;height:50px"></div>
				<input id="small" type="text" style="font-size:14px">
			</body>`);
		const report = await collectLayoutReport(page);
		const vw = page.viewportSize()?.width ?? 0;
		expect(report.scrollWidth).toBeGreaterThan(vw);
		expect(report.offenders).toHaveLength(1);
		expect(report.offenders[0].width).toBe(2000);
		expect(report.smallInputs).toHaveLength(1);
		expect(report.smallInputs[0].fontSize).toBe(14);
	});

	test("a page that fits reports nothing", async ({ page }) => {
		await page.setContent(`
			<meta name="viewport" content="width=device-width, initial-scale=1">
			<body style="margin:0"><p>Fits.</p><input type="text" style="font-size:16px"></body>`);
		const report = await collectLayoutReport(page);
		expect(report.scrollWidth).toBeLessThanOrEqual(report.clientWidth);
		expect(report.offenders).toEqual([]);
		expect(report.smallInputs).toEqual([]);
	});
});
