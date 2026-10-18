/**
 * A Markdown report split into the sections its table of contents lists.
 *
 * Mirrors app/notebooks/studio/report_view/sections.py, so a suggestion the
 * backend stored against section n sits under the section shown as n: a
 * leading `#` heading is the title; the listed level is the shallowest heading
 * level occurring at least twice after it (else the shallowest present);
 * headings inside fenced code are not headings.
 */

export interface ReportSection {
	index: number;
	heading: string;
	/** The section's Markdown, heading line included. */
	markdown: string;
}

export interface ReportOutline {
	title: string | null;
	preamble: string;
	sections: ReportSection[];
}

const HEADING = /^(#{1,6})[ \t]+(.+?)[ \t]*#*[ \t]*$/;
const FENCE = /^[ \t]{0,3}(`{3,}|~{3,})/;
// "1. ", "2) ", "1.2 ", "IV. ": not a bare number ("2024 in review").
const NUMBERING = /^(?:\d+(?:\.\d+)*[.)]|\d+(?:\.\d+)+|[IVXLC]+\.)\s+/;
const EMPHASIS = /[*_`]+/g;

/** The heading as the contents list shows it: no emphasis, no numbering. */
export function headingLabel(raw: string): string {
	const text = raw.replace(EMPHASIS, "").trim();
	const stripped = text.replace(NUMBERING, "").trim();
	return stripped || text;
}

function headings(lines: string[]): { line: number; level: number; text: string }[] {
	const found: { line: number; level: number; text: string }[] = [];
	let fence: string | null = null;
	lines.forEach((line, number) => {
		const marker = FENCE.exec(line);
		if (marker) {
			const token = marker[1];
			if (fence === null) fence = token;
			else if (token[0] === fence[0] && token.length >= fence.length) fence = null;
			return;
		}
		if (fence !== null) return;
		const match = HEADING.exec(line);
		if (match) found.push({ line: number, level: match[1].length, text: match[2].trim() });
	});
	return found;
}

export function splitReport(markdown: string): ReportOutline {
	const lines = (markdown ?? "").replace(/\r\n/g, "\n").split("\n");
	let found = headings(lines);
	let title: string | null = null;
	let titleLine: number | null = null;
	if (found.length && found[0].level === 1) {
		const before = lines.slice(0, found[0].line).join("\n").trim();
		if (!before) {
			titleLine = found[0].line;
			title = headingLabel(found[0].text);
			found = found.slice(1);
		}
	}
	const start = titleLine === null ? 0 : titleLine + 1;
	if (!found.length) {
		return { title, preamble: lines.slice(start).join("\n").trim(), sections: [] };
	}
	const counts = new Map<number, number>();
	for (const h of found) counts.set(h.level, (counts.get(h.level) ?? 0) + 1);
	const repeated = [...counts].filter(([, count]) => count >= 2).map(([level]) => level);
	const level = repeated.length ? Math.min(...repeated) : Math.min(...counts.keys());
	const starts = found.filter((h) => h.level === level);
	const preamble = lines.slice(start, starts[0].line).join("\n").trim();
	const sections = starts.map((h, index) => {
		// Up to the next listed heading, so nothing between two is dropped.
		const end = index + 1 < starts.length ? starts[index + 1].line : lines.length;
		return {
			index,
			heading: headingLabel(h.text),
			markdown: lines.slice(h.line, end).join("\n").trim(),
		};
	});
	return { title, preamble, sections };
}

/**
 * The reading view is for reports with real text; a stub ("# Title\n\nE2E
 * report.") or an empty rendition shows the report's own file instead.
 */
export function isReadable(outline: ReportOutline): boolean {
	const body = outline.sections.map((s) => s.markdown).join("\n") + outline.preamble;
	return outline.sections.length > 0 || body.trim().length >= 400;
}

/**
 * Scroll-spy: the section being read is the last one whose top has passed the
 * reading line (`offset` below the scroller's top). Before the first section
 * that is the first one; at the very bottom of the scroller it is the last,
 * so a short final section can still be highlighted.
 *
 * `tops` are each section's top relative to the scroller's visible top.
 */
export function activeSectionIndex(
	tops: readonly number[],
	{ offset, atBottom }: { offset: number; atBottom: boolean }
): number {
	if (!tops.length) return -1;
	if (atBottom) return tops.length - 1;
	let active = 0;
	for (let i = 0; i < tops.length; i++) {
		if (tops[i] <= offset) active = i;
		else break;
	}
	return active;
}
