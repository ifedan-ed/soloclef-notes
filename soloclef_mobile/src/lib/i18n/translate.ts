/**
 * Message lookup: the web's namespaces (notebooks.*, auth.*, common.*,
 * upload_documents.*) plus the app's own ``mobile.*``, in the chosen locale
 * with English as the fallback for any key a locale lacks.
 */
import en from "./messages/en.json";
import es from "./messages/es.json";
import hi from "./messages/hi.json";
import ko from "./messages/ko.json";
import pt from "./messages/pt.json";
import zh from "./messages/zh.json";
import { BRAND_NAME } from "../brand";
import { formatMessage, type Values } from "./icu";
import { mobileEn } from "./mobile-en";
import { mobileTranslations } from "./mobile-translations";

export const SUPPORTED_LOCALES = ["en", "es", "pt", "hi", "ko", "zh"] as const;
export type Locale = (typeof SUPPORTED_LOCALES)[number];

/** Each language named in itself, for the language picker. */
export const LOCALE_NAMES: Record<Locale, string> = {
	en: "English",
	es: "Español",
	pt: "Português",
	hi: "हिन्दी",
	ko: "한국어",
	zh: "中文",
};

type Tree = { [key: string]: string | Tree };

const WEB: Record<Locale, Tree> = { en, es, pt, hi, ko, zh } as unknown as Record<Locale, Tree>;

function lookup(tree: Tree | undefined, key: string): string | undefined {
	let node: string | Tree | undefined = tree;
	for (const part of key.split(".")) {
		if (typeof node !== "object" || node === null) return undefined;
		node = node[part];
	}
	return typeof node === "string" ? node : undefined;
}

export function resolveLocale(preferred: string, deviceTags: readonly string[]): Locale {
	const candidates = preferred ? [preferred, ...deviceTags] : [...deviceTags];
	for (const tag of candidates) {
		const base = tag.toLowerCase().split(/[-_]/)[0] as Locale;
		if ((SUPPORTED_LOCALES as readonly string[]).includes(base)) return base;
	}
	return "en";
}

export type Translate = (key: string, values?: Values) => string;

export function createTranslator(locale: Locale): Translate {
	return (key, values) => {
		let message: string | undefined;
		if (key.startsWith("mobile.")) {
			const short = key.slice("mobile.".length) as keyof typeof mobileEn;
			message = mobileTranslations[locale]?.[short] ?? mobileEn[short];
		} else {
			message = lookup(WEB[locale], key) ?? lookup(WEB.en, key);
		}
		// A missing key shows itself, as next-intl does in development.
		if (message === undefined) return key;
		// The shared locale files say {brand} where the product name goes.
		return formatMessage(message, { brand: BRAND_NAME, ...values }, locale);
	};
}

/** Whether a key exists (to fall back when the web has no label for a value). */
export function hasMessage(locale: Locale, key: string): boolean {
	if (key.startsWith("mobile.")) return key.slice(7) in mobileEn;
	return lookup(WEB[locale], key) !== undefined || lookup(WEB.en, key) !== undefined;
}
