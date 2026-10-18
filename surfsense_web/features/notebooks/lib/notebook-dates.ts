/** "Sep 23, 2026" in the reader's locale; an unparseable date yields "". */
export function formatNotebookDate(iso: string | null | undefined, locale: string): string {
	if (!iso) return "";
	const date = new Date(iso);
	if (Number.isNaN(date.getTime())) return "";
	return new Intl.DateTimeFormat(locale, { dateStyle: "medium" }).format(date);
}
