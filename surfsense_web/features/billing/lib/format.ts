/**
 * Number, money and date formatting for billing screens. Pure functions of the
 * UI locale, so the same figures read the same in every language.
 */

/** Credits are display units (1 credit = 1 US cent); show up to two decimals. */
export function formatCredits(credits: number, locale: string): string {
	return new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(credits);
}

/** An amount in the currency's minor unit (cents) as localized money. */
export function formatMoney(cents: number, currency: string, locale: string): string {
	const code = (currency || "usd").toUpperCase();
	const whole = cents % 100 === 0;
	try {
		return new Intl.NumberFormat(locale, {
			style: "currency",
			currency: code,
			minimumFractionDigits: whole ? 0 : 2,
			maximumFractionDigits: 2,
		}).format(cents / 100);
	} catch {
		return `${(cents / 100).toFixed(2)} ${code}`;
	}
}

/** A date (ISO string) in the reader's language, without the time. */
export function formatDate(iso: string | null | undefined, locale: string): string {
	if (!iso) return "";
	const date = new Date(iso);
	if (Number.isNaN(date.getTime())) return "";
	return new Intl.DateTimeFormat(locale, { dateStyle: "medium" }).format(date);
}

/** A date and time (ISO string) in the reader's language. */
export function formatDateTime(iso: string | null | undefined, locale: string): string {
	if (!iso) return "";
	const date = new Date(iso);
	if (Number.isNaN(date.getTime())) return "";
	return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(date);
}

/** Share of the plan allowance still left, 0-100, for a progress bar. */
export function allowancePercent(left: number, allowance: number): number {
	if (allowance <= 0) return 0;
	return Math.round(Math.min(100, Math.max(0, (left / allowance) * 100)));
}

/** Seconds as m:ss (or h:mm:ss past an hour) for a countdown. */
export function formatCountdown(totalSeconds: number): string {
	const s = Math.max(0, Math.floor(totalSeconds));
	const hours = Math.floor(s / 3600);
	const minutes = Math.floor((s % 3600) / 60);
	const seconds = s % 60;
	const pad = (n: number) => String(n).padStart(2, "0");
	return hours > 0 ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${minutes}:${pad(seconds)}`;
}

/** Micros per credit, for settings stored in micros (signup_bonus_micros). */
export const MICROS_PER_CREDIT = 10_000;

export function creditsToMicros(credits: number): number {
	return Math.round(credits * MICROS_PER_CREDIT);
}

export function microsToCredits(micros: number): number {
	return micros / MICROS_PER_CREDIT;
}

/** Whole-number input parsing that treats blanks and junk as a fallback. */
export function parseNumber(value: string, fallback = 0): number {
	const n = Number(value.trim().replace(",", "."));
	return Number.isFinite(n) ? n : fallback;
}

/** "9.00" -> 900 cents. */
export function dollarsToCents(value: string): number {
	return Math.round(parseNumber(value) * 100);
}

export function centsToDollars(cents: number): string {
	return (cents / 100).toFixed(2);
}
