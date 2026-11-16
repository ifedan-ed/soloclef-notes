import assert from "node:assert/strict";
import { Buffer } from "node:buffer";
import test from "node:test";
import ExcelJS from "exceljs";

import {
	MAX_DISPLAY_ROWS,
	MAX_VIEWER_BYTES,
	ParseWorkbookError,
	parseWorkbook,
} from "@/features/file-viewers/parse-workbook";
import { gridColumnWidth } from "@/features/file-viewers/grid-column-width";

async function workbookBytes(
	build: (wb: ExcelJS.Workbook) => void | Promise<void>
): Promise<ArrayBuffer> {
	const wb = new ExcelJS.Workbook();
	await build(wb);
	const buffer = Buffer.from(await wb.xlsx.writeBuffer());
	return buffer.buffer.slice(
		buffer.byteOffset,
		buffer.byteOffset + buffer.byteLength
	) as ArrayBuffer;
}

test("parseWorkbook formats values and formula caches", async () => {
	const data = await workbookBytes((wb) => {
		const sheet = wb.addWorksheet("Budget");
		sheet.getCell("A1").value = "Item";
		sheet.getCell("B1").value = "Amount";
		sheet.getCell("A2").value = "Widgets";
		sheet.getCell("B2").value = 12.5;
		sheet.getCell("B2").numFmt = "0.00";
		sheet.getCell("B3").value = { formula: "B2*2", result: 25 };
		sheet.getCell("B3").numFmt = "0.00";
	});

	const view = await parseWorkbook(data);
	assert.equal(view.sheets.length, 1);
	assert.equal(view.sheets[0].name, "Budget");
	assert.equal(view.sheets[0].truncated, false);
	assert.equal(view.sheets[0].cells[0][0].text, "Item");
	assert.equal(view.sheets[0].cells[1][1].text, "12.50");
	assert.equal(view.sheets[0].cells[2][1].text, "25.00");
});

test("parseWorkbook caps displayed rows", async () => {
	const data = await workbookBytes((wb) => {
		const sheet = wb.addWorksheet("Big");
		for (let row = 1; row <= MAX_DISPLAY_ROWS + 3; row += 1) {
			sheet.getCell(row, 1).value = row;
		}
	});

	const view = await parseWorkbook(data);
	assert.equal(view.sheets[0].cells.length, MAX_DISPLAY_ROWS);
	assert.equal(view.sheets[0].truncated, true);
});

test("parseWorkbook rejects oversize payloads before parsing", async () => {
	const huge = new ArrayBuffer(MAX_VIEWER_BYTES + 1);
	await assert.rejects(
		() => parseWorkbook(huge),
		(error: unknown) => error instanceof ParseWorkbookError && error.code === "oversize"
	);
});

test("parseWorkbook rejects corrupt workbooks", async () => {
	const corrupt = Buffer.from("not-a-workbook").buffer;
	await assert.rejects(
		() => parseWorkbook(corrupt),
		(error: unknown) => error instanceof ParseWorkbookError && error.code === "corrupt"
	);
});

test("parseWorkbook keeps each column's width, null where the workbook sets none", async () => {
	const data = await workbookBytes((wb) => {
		const sheet = wb.addWorksheet("Overview");
		sheet.getColumn(1).width = 26;
		sheet.getCell("A1").value = "A long title that runs well past its column";
		sheet.getCell("B5").value = "Value";
		sheet.getCell("C5").value = "Plain";
		sheet.getColumn(2).width = 104;
	});

	const view = await parseWorkbook(data);
	assert.deepEqual(view.sheets[0].widths, [26, 104, null]);
});

test("grid columns follow the workbook's widths within bounds", () => {
	assert.equal(gridColumnWidth(26), 192);
	assert.equal(gridColumnWidth(null), 69);
	assert.equal(gridColumnWidth(104), 420);
	assert.equal(gridColumnWidth(2), 64);
});

test("a merged range's value shows once, in its first cell", async () => {
	const data = await workbookBytes((wb) => {
		const sheet = wb.addWorksheet("Overview");
		sheet.getCell("A1").value = "Title";
		sheet.mergeCells("A1:C1");
		sheet.getCell("A2").value = "a";
		sheet.getCell("B2").value = "b";
	});

	const view = await parseWorkbook(data);
	assert.deepEqual(
		view.sheets[0].cells[0].map((cell) => cell.text),
		["Title", "", ""]
	);
	assert.deepEqual(
		view.sheets[0].cells[1].map((cell) => cell.text),
		["a", "b", ""]
	);
});
