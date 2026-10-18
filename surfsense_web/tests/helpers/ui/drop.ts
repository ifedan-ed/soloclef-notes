import type { Locator, Page } from "@playwright/test";

/**
 * Drop files from the "desktop" onto an element: the drag events a browser
 * fires, carrying a real DataTransfer, so react-dropzone sees a file drop.
 */
export async function dropFiles(
	page: Page,
	target: Locator,
	files: { name: string; type: string; text: string }[]
): Promise<void> {
	const dataTransfer = await page.evaluateHandle((list) => {
		const dt = new DataTransfer();
		for (const f of list) dt.items.add(new File([f.text], f.name, { type: f.type }));
		return dt;
	}, files);
	for (const type of ["dragenter", "dragover", "drop"] as const) {
		await target.dispatchEvent(type, { dataTransfer });
	}
}
