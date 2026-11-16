import ExcelJS from "exceljs";
import * as SSF from "ssf";

/** Soft ceiling for in-browser grid rows per sheet (full file still downloads). */
export const MAX_DISPLAY_ROWS = 500;

/** Reject before ExcelJS allocates — keep below the server file limit. */
export const MAX_VIEWER_BYTES = 15 * 1024 * 1024;

export type ParseWorkbookErrorCode = "oversize" | "corrupt";

export class ParseWorkbookError extends Error {
	readonly code: ParseWorkbookErrorCode;

	constructor(code: ParseWorkbookErrorCode, message: string) {
		super(message);
		this.name = "ParseWorkbookError";
		this.code = code;
	}
}

export interface CellView {
	text: string;
}

export interface SheetView {
	name: string;
	cells: CellView[][];
	truncated: boolean;
	/**
	 * Each column's width as the workbook sets it, in characters, or null
	 * where it sets none. The grid sizes columns from these: measured from
	 * their text instead, one long title in a column's first rows made that
	 * column wider than the viewer and pushed every other one out of sight.
	 */
	widths: (number | null)[];
}

export interface WorkbookView {
	sheets: SheetView[];
}

function cellText(cell: ExcelJS.Cell): string {
	const value = cell.value;
	if (value == null || value === "") return "";

	if (typeof value === "object" && value !== null && "richText" in value) {
		return (value as ExcelJS.CellRichTextValue).richText.map((part) => part.text).join("");
	}
	if (typeof value === "object" && value !== null && "text" in value && "hyperlink" in value) {
		return String((value as ExcelJS.CellHyperlinkValue).text ?? "");
	}
	if (typeof value === "object" && value !== null && "error" in value) {
		return String((value as ExcelJS.CellErrorValue).error);
	}
	if (typeof value === "object" && value !== null && "formula" in value) {
		const formula = value as ExcelJS.CellFormulaValue | ExcelJS.CellSharedFormulaValue;
		const result = "result" in formula ? formula.result : undefined;
		if (result == null || result === "") return "";
		return formatValue(result, cell.numFmt);
	}
	return formatValue(value, cell.numFmt);
}

function formatValue(value: ExcelJS.CellValue, numFmt: string | undefined): string {
	if (value instanceof Date) {
		try {
			return SSF.format(numFmt || "yyyy-mm-dd", excelSerialFromDate(value));
		} catch {
			return value.toISOString().slice(0, 10);
		}
	}
	if (typeof value === "number") {
		try {
			return SSF.format(numFmt || "General", value);
		} catch {
			return String(value);
		}
	}
	if (typeof value === "boolean") return value ? "TRUE" : "FALSE";
	if (typeof value === "string") return value;
	return String(value ?? "");
}

function excelSerialFromDate(date: Date): number {
	// Excel serial day count (Windows epoch); good enough for display formatting.
	return date.getTime() / 86_400_000 + 25569;
}

function columnWidths(sheet: ExcelJS.Worksheet, colCount: number): (number | null)[] {
	return Array.from({ length: colCount }, (_, index) => {
		const width = sheet.getColumn(index + 1).width;
		return typeof width === "number" && width > 0 ? width : null;
	});
}

function sheetMatrix(sheet: ExcelJS.Worksheet): {
	cells: CellView[][];
	truncated: boolean;
	widths: (number | null)[];
} {
	const rowCount = sheet.rowCount || 0;
	const colCount = sheet.columnCount || 0;
	const displayRows = Math.min(rowCount, MAX_DISPLAY_ROWS);
	const cells: CellView[][] = [];
	for (let r = 1; r <= displayRows; r += 1) {
		const row: CellView[] = [];
		for (let c = 1; c <= colCount; c += 1) {
			const cell = sheet.getCell(r, c);
			// ExcelJS gives every cell of a merged range the range's value, so a
			// title merged across A1:K1 was shown eleven times. Only the range's
			// first cell holds it, as in a spreadsheet.
			const covered = cell.isMerged && cell.master.address !== cell.address;
			row.push({ text: covered ? "" : cellText(cell) });
		}
		cells.push(row);
	}
	return { cells, truncated: rowCount > MAX_DISPLAY_ROWS, widths: columnWidths(sheet, colCount) };
}

export async function parseWorkbook(data: ArrayBuffer): Promise<WorkbookView> {
	if (data.byteLength > MAX_VIEWER_BYTES) {
		throw new ParseWorkbookError(
			"oversize",
			`Workbook is ${(data.byteLength / (1024 * 1024)).toFixed(1)} MB; preview limit is ${
				MAX_VIEWER_BYTES / (1024 * 1024)
			} MB`
		);
	}

	const workbook = new ExcelJS.Workbook();
	try {
		await workbook.xlsx.load(data);
	} catch {
		throw new ParseWorkbookError("corrupt", "This workbook could not be opened");
	}

	if (workbook.worksheets.length === 0) {
		throw new ParseWorkbookError("corrupt", "This workbook has no worksheets");
	}

	return {
		sheets: workbook.worksheets.map((sheet) => {
			const { cells, truncated, widths } = sheetMatrix(sheet);
			return { name: sheet.name, cells, truncated, widths };
		}),
	};
}
