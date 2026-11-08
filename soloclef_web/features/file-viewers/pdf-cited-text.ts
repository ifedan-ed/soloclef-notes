// Where a cited chunk's text sits on a PDF page, from that page's text items
// (pdf.js getTextContent). Pure: no pdf.js import, so it is unit tested alone.
//
// The chunk is markdown cut from the parser's output and the page is the PDF's
// text layer, so they are compared as runs of lower-case words, ignoring
// markdown punctuation.

/** A pdf.js text item: a run of text the page draws in one go. */
export interface PageTextItem {
	str: string;
}

/** Inclusive range of item indexes the chunk's text covers on the page. */
export interface ItemRange {
	first: number;
	last: number;
}

const WORD = /[\p{L}\p{N}]+/gu;
// Words in a row that must match before a position counts as the chunk's start.
const ANCHOR_WORDS = 4;
// How far into the chunk to look for an anchor (a chunk may open with a
// heading the text layer lays out elsewhere).
const MAX_ANCHOR_OFFSET = 40;
// Chunk words compared at most; enough to cover a chunk on one page.
const MAX_CHUNK_WORDS = 400;
// Words skipped on either side to get back in step (a hyphenated word, a
// table cell read in another order) before the match is taken to have ended.
const MAX_RESYNC_SKIP = 3;
const MAX_RESYNCS = 12;

export function textWords(text: string): string[] {
	return (text.match(WORD) ?? []).map((word) => word.toLowerCase());
}

/** 1-based page to open at, or null when there is none or it is out of range. */
export function pageToOpen(
	initialPage: number | null | undefined,
	numPages: number
): number | null {
	if (!initialPage || !Number.isInteger(initialPage)) return null;
	if (initialPage < 1 || initialPage > numPages) return null;
	return initialPage;
}

/** The items on a page that carry the start of ``chunkText``, or null if it is not there. */
export function findTextOnPage(
	items: readonly PageTextItem[],
	chunkText: string
): ItemRange | null {
	const pageWords: string[] = [];
	const itemOfWord: number[] = [];
	items.forEach((item, index) => {
		for (const word of textWords(item.str)) {
			pageWords.push(word);
			itemOfWord.push(index);
		}
	});
	const chunkWords = textWords(chunkText).slice(0, MAX_CHUNK_WORDS);
	if (!pageWords.length || !chunkWords.length) return null;

	const anchor = Math.min(ANCHOR_WORDS, chunkWords.length);
	const lastOffset = Math.min(MAX_ANCHOR_OFFSET, chunkWords.length - anchor);
	for (let offset = 0; offset <= lastOffset; offset++) {
		for (let start = 0; start + anchor <= pageWords.length; start++) {
			if (!matchesAt(pageWords, start, chunkWords, offset, anchor)) continue;
			const end = extendMatch(pageWords, start + anchor, chunkWords, offset + anchor);
			return { first: itemOfWord[start], last: itemOfWord[end] };
		}
	}
	return null;
}

function matchesAt(
	page: readonly string[],
	pageAt: number,
	chunk: readonly string[],
	chunkAt: number,
	length: number
): boolean {
	for (let i = 0; i < length; i++) {
		if (page[pageAt + i] !== chunk[chunkAt + i]) return false;
	}
	return true;
}

/** Index of the last page word still in step with the chunk. */
function extendMatch(
	page: readonly string[],
	pageAt: number,
	chunk: readonly string[],
	chunkAt: number
): number {
	let p = pageAt;
	let c = chunkAt;
	let lastMatched = pageAt - 1;
	let resyncs = 0;
	while (p < page.length && c < chunk.length) {
		if (page[p] === chunk[c]) {
			lastMatched = p;
			p++;
			c++;
			continue;
		}
		const step = resync(page, p, chunk, c);
		if (!step || ++resyncs > MAX_RESYNCS) break;
		p += step[0];
		c += step[1];
	}
	return lastMatched;
}

/** Words to skip on the page and in the chunk to match again, or null. */
function resync(
	page: readonly string[],
	p: number,
	chunk: readonly string[],
	c: number
): [number, number] | null {
	for (let total = 1; total <= MAX_RESYNC_SKIP * 2; total++) {
		for (let skipPage = 0; skipPage <= Math.min(total, MAX_RESYNC_SKIP); skipPage++) {
			const skipChunk = total - skipPage;
			if (skipChunk > MAX_RESYNC_SKIP) continue;
			// Two words in step, so a common word ("the") does not fake a resync.
			if (
				page[p + skipPage] !== undefined &&
				page[p + skipPage] === chunk[c + skipChunk] &&
				page[p + skipPage + 1] === chunk[c + skipChunk + 1]
			) {
				return [skipPage, skipChunk];
			}
		}
	}
	return null;
}

/** A text item with its placement in PDF user space, as pdf.js reports it. */
export interface PlacedTextItem extends PageTextItem {
	transform: readonly number[];
	width: number;
	height: number;
}

/** The part of a pdf.js page viewport that maps PDF space to the page box. */
export interface PageViewportLike {
	width: number;
	height: number;
	convertToViewportRectangle(rect: number[]): number[];
}

/** A rectangle as percentages of the page box, so it follows every zoom level. */
export interface PercentRect {
	left: number;
	top: number;
	width: number;
	height: number;
}

/** Page-relative rectangles covering the items in ``range``. */
export function citedRects(
	items: readonly PlacedTextItem[],
	range: ItemRange,
	viewport: PageViewportLike
): PercentRect[] {
	const rects: PercentRect[] = [];
	for (let i = range.first; i <= range.last; i++) {
		const item = items[i];
		if (!item || !item.str.trim()) continue;
		const x = item.transform[4];
		const y = item.transform[5];
		const height = item.height || Math.hypot(item.transform[2], item.transform[3]);
		const [x1, y1, x2, y2] = viewport.convertToViewportRectangle([
			x,
			y,
			x + item.width,
			y + height,
		]);
		rects.push({
			left: (Math.min(x1, x2) / viewport.width) * 100,
			top: (Math.min(y1, y2) / viewport.height) * 100,
			width: (Math.abs(x2 - x1) / viewport.width) * 100,
			height: (Math.abs(y2 - y1) / viewport.height) * 100,
		});
	}
	return rects;
}
