// A character of a workbook's column width, in pixels at the grid's text
// size, and the bounds a column is kept within so that one wide column
// never hides the rest and a narrow one still shows its header.
const PX_PER_CHAR = 7;
const MIN_COLUMN_PX = 64;
const MAX_COLUMN_PX = 420;
// A column the workbook gives no width: as Excel's default (8.43 characters).
const DEFAULT_COLUMN_CHARS = 8.43;

/** A column's width in the grid, from its width in the workbook. */
export function gridColumnWidth(chars: number | null): number {
	const px = Math.round((chars ?? DEFAULT_COLUMN_CHARS) * PX_PER_CHAR + 10);
	return Math.min(MAX_COLUMN_PX, Math.max(MIN_COLUMN_PX, px));
}
