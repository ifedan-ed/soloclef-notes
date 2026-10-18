const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
	["year", 365 * 24 * 3600],
	["month", 30 * 24 * 3600],
	["week", 7 * 24 * 3600],
	["day", 24 * 3600],
	["hour", 3600],
	["minute", 60],
];

/** "3 days ago" / "hace 3 días" — localized, coarse, never in the future. */
export function relativeTime(iso: string, locale: string, now: Date = new Date()): string {
	const seconds = Math.max(0, (now.getTime() - new Date(iso).getTime()) / 1000);
	const format = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
	for (const [unit, size] of UNITS) {
		if (seconds >= size) return format.format(-Math.floor(seconds / size), unit);
	}
	return format.format(0, "second");
}

export const NOTEBOOK_ICONS = [
	"📓",
	"📚",
	"🧪",
	"🧠",
	"🗂️",
	"🔬",
	"💡",
	"🧭",
	"🩺",
	"📊",
	"🌍",
	"✏️",
];

export function defaultNotebookIcon(seed: number): string {
	return NOTEBOOK_ICONS[Math.abs(seed) % NOTEBOOK_ICONS.length];
}
