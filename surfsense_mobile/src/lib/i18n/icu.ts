/**
 * The slice of ICU MessageFormat the web's messages/*.json use (next-intl):
 * ``{name}`` arguments, ``{n, plural, =0 {…} one {# …} other {…}}`` with
 * ``#`` for the number, and ``{x, select, a {…} other {…}}``. Nested
 * messages inside branches are formatted recursively. Hermes has no reliable
 * Intl.PluralRules, so plural categories come from a small per-locale table
 * covering the app's six languages.
 */

export type Values = Record<string, string | number | null | undefined>;

type PluralCategory = "zero" | "one" | "two" | "few" | "many" | "other";

export function pluralCategory(locale: string, n: number): PluralCategory {
	const lang = locale.toLowerCase().split(/[-_]/)[0];
	switch (lang) {
		case "zh":
		case "ko":
		case "ja":
			return "other";
		case "hi":
			// Hindi: 0 and 1 are "one".
			return n >= 0 && n <= 1 ? "one" : "other";
		case "pt":
			// CLDR (pt-BR): 0 and 1 are "one" for integers.
			return Number.isInteger(n) && (n === 0 || n === 1) ? "one" : "other";
		case "fr":
			return n >= 0 && n < 2 ? "one" : "other";
		default:
			// en, es, de, it, …
			return n === 1 ? "one" : "other";
	}
}

/** Index of the brace that closes the one opened at ``open``. */
function matchingBrace(text: string, open: number): number {
	let depth = 0;
	for (let i = open; i < text.length; i += 1) {
		const ch = text[i];
		if (ch === "'" && (text[i + 1] === "{" || text[i + 1] === "}")) {
			// Quoted literal brace: skip to the closing quote.
			const close = text.indexOf("'", i + 1);
			if (close > 0) {
				i = close;
				continue;
			}
		}
		if (ch === "{") depth += 1;
		else if (ch === "}") {
			depth -= 1;
			if (depth === 0) return i;
		}
	}
	return -1;
}

/** ``=0 {…} one {…} other {…}`` into a map of selector → branch text. */
function parseBranches(text: string): Map<string, string> {
	const branches = new Map<string, string>();
	let i = 0;
	while (i < text.length) {
		while (i < text.length && /\s/.test(text[i])) i += 1;
		const open = text.indexOf("{", i);
		if (open < 0) break;
		const selector = text.slice(i, open).trim();
		const close = matchingBrace(text, open);
		if (close < 0) break;
		branches.set(selector, text.slice(open + 1, close));
		i = close + 1;
	}
	return branches;
}

function formatArgument(inner: string, values: Values, locale: string): string {
	const firstComma = inner.indexOf(",");
	if (firstComma < 0) {
		const value = values[inner.trim()];
		return value === null || value === undefined ? `{${inner.trim()}}` : String(value);
	}
	const name = inner.slice(0, firstComma).trim();
	const rest = inner.slice(firstComma + 1);
	const secondComma = rest.indexOf(",");
	const type = (secondComma < 0 ? rest : rest.slice(0, secondComma)).trim();
	const body = secondComma < 0 ? "" : rest.slice(secondComma + 1);
	const value = values[name];

	if (type === "plural") {
		const n = Number(value ?? 0);
		const branches = parseBranches(body);
		const branch = branches.get(`=${n}`) ?? branches.get(pluralCategory(locale, n)) ?? branches.get("other") ?? "";
		return formatMessage(branch.replace(/#/g, String(n)), values, locale);
	}
	if (type === "select") {
		const branches = parseBranches(body);
		const branch = branches.get(String(value)) ?? branches.get("other") ?? "";
		return formatMessage(branch, values, locale);
	}
	return value === null || value === undefined ? "" : String(value);
}

export function formatMessage(message: string, values: Values = {}, locale = "en"): string {
	let out = "";
	let i = 0;
	while (i < message.length) {
		const ch = message[i];
		if (ch === "'" && (message[i + 1] === "{" || message[i + 1] === "}")) {
			const close = message.indexOf("'", i + 1);
			if (close > 0) {
				out += message.slice(i + 1, close);
				i = close + 1;
				continue;
			}
		}
		if (ch === "{") {
			const close = matchingBrace(message, i);
			if (close < 0) {
				out += message.slice(i);
				break;
			}
			out += formatArgument(message.slice(i + 1, close), values, locale);
			i = close + 1;
			continue;
		}
		out += ch;
		i += 1;
	}
	return out;
}
