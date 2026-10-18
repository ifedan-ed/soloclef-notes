/**
 * @jest-environment node
 */
import { execFileSync } from "node:child_process";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { KNOWN_PHASES, STUDIO_TILES } from "../../studio/catalog";
import { formatMessage, pluralCategory } from "../icu";
import { mobileEn } from "../mobile-en";
import { mobileTranslations } from "../mobile-translations";
import { BRAND_NAME } from "../../brand";
import { createTranslator, hasMessage, resolveLocale, SUPPORTED_LOCALES } from "../translate";

describe("ICU subset", () => {
	it("formats arguments, plurals with # and =N, and selects", () => {
		const count = "{count, plural, =0 {No sources} one {# source} other {# sources}}";
		expect(formatMessage(count, { count: 0 })).toBe("No sources");
		expect(formatMessage(count, { count: 1 })).toBe("1 source");
		expect(formatMessage(count, { count: 7 })).toBe("7 sources");
		expect(formatMessage("Delete “{title}”?", { title: "GINA" })).toBe("Delete “GINA”?");
		expect(formatMessage("{g, select, a {Alpha} other {Other}}", { g: "a" })).toBe("Alpha");
		expect(formatMessage("Upload {count} {count, plural, one {file} other {files}}", { count: 2 })).toBe(
			"Upload 2 files"
		);
	});

	it("leaves unknown arguments visible and keeps quoted braces", () => {
		expect(formatMessage("Hi {name}")).toBe("Hi {name}");
		expect(formatMessage("Use '{'braces'}'")).toBe("Use {braces}");
	});

	it("uses each language's plural categories", () => {
		expect(pluralCategory("en", 1)).toBe("one");
		expect(pluralCategory("en", 0)).toBe("other");
		expect(pluralCategory("hi", 0)).toBe("one");
		expect(pluralCategory("pt", 0)).toBe("one");
		expect(pluralCategory("ko", 1)).toBe("other");
		expect(pluralCategory("zh", 5)).toBe("other");
	});
});

describe("translator", () => {
	it("resolves the preferred language, then the device's, then English", () => {
		expect(resolveLocale("es", ["ko-KR"])).toBe("es");
		expect(resolveLocale("", ["pt-BR", "en-US"])).toBe("pt");
		expect(resolveLocale("", ["fr-FR"])).toBe("en");
	});

	it("reads the web's messages and the app's own, with English fallback", () => {
		const es = createTranslator("es");
		expect(es("notebooks.tab_all")).not.toBe("notebooks.tab_all");
		expect(es("mobile.sign_out")).toBe("Cerrar sesión");
		expect(createTranslator("en")("notebooks.source_count", { count: 2 })).toBe("2 sources");
		expect(createTranslator("en")("no.such.key")).toBe("no.such.key");
	});

	it("fills the product name into the shared strings in every language", () => {
		for (const locale of SUPPORTED_LOCALES) {
			const title = createTranslator(locale)("auth.signups_closed_title");
			expect(title).not.toBe("auth.signups_closed_title");
			const appName = createTranslator(locale)("common.app_name");
			expect(appName).toBe(BRAND_NAME);
			expect(createTranslator(locale)("auth.register_subtitle")).toContain(BRAND_NAME);
		}
	});

	it("has every app-only string in every language", () => {
		for (const locale of SUPPORTED_LOCALES.filter((l) => l !== "en")) {
			const missing = Object.keys(mobileEn).filter((key) => !(key in (mobileTranslations[locale] ?? {})));
			// link_placeholder is a URL hint and stays as is.
			expect({ locale, missing: missing.filter((k) => k !== "link_placeholder") }).toEqual({ locale, missing: [] });
		}
	});

	it("keeps {placeholders} identical across translations", () => {
		const names = (s: string) => [...s.matchAll(/\{(\w+)/g)].map((m) => m[1]).sort();
		for (const [locale, table] of Object.entries(mobileTranslations)) {
			for (const [key, value] of Object.entries(table)) {
				expect({ locale, key, names: names(value ?? "") }).toEqual({
					locale,
					key,
					names: names(mobileEn[key as keyof typeof mobileEn]),
				});
			}
		}
	});
});

/** Every literal t("…") key in the app's source resolves in English. */
describe("message keys used by the app exist", () => {
	const root = join(__dirname, "..", "..", "..");
	const files: string[] = [];
	const walk = (dir: string) => {
		for (const name of readdirSync(dir)) {
			const path = join(dir, name);
			if (name === "__tests__") continue;
			if (statSync(path).isDirectory()) walk(path);
			else if (/\.tsx?$/.test(name)) files.push(path);
		}
	};
	walk(root);
	const keys = new Set<string>();
	for (const file of files) {
		for (const match of readFileSync(file, "utf8").matchAll(/\bt\(\s*"([a-z_]+\.[\w.]+)"/g)) keys.add(match[1]);
	}

	it("finds the keys", () => {
		expect(keys.size).toBeGreaterThan(80);
	});

	it.each([...keys].sort())("%s", (key) => {
		expect(hasMessage("en", key)).toBe(true);
	});

	it("has labels for every Studio kind, option, field and phase", () => {
		const missing: string[] = [];
		for (const tile of STUDIO_TILES) {
			for (const suffix of [
				"label",
				"dialog_title",
				"description",
				"focus_label",
				"focus_placeholder",
				"ideas_label",
				"tooltip",
			]) {
				const key = `notebooks.kinds.${tile.kind}.${suffix}`;
				if (!hasMessage("en", key)) missing.push(key);
			}
			for (const group of tile.groups) {
				if (!hasMessage("en", `notebooks.fields.${group.field}`)) missing.push(`notebooks.fields.${group.field}`);
				for (const value of group.values) {
					const key = `notebooks.options.${tile.kind}.${group.field}.${value}.label`;
					if (!hasMessage("en", key)) missing.push(key);
				}
			}
		}
		for (const phase of KNOWN_PHASES) if (!hasMessage("en", `notebooks.phases.${phase}`)) missing.push(phase);
		for (const status of ["queued", "running", "ready", "failed", "cancelled"]) {
			if (!hasMessage("en", `notebooks.status.${status}`)) missing.push(status);
		}
		expect(missing).toEqual([]);
	});
});

describe("synced web messages", () => {
	it("match surfsense_web/messages (run npm run sync-messages)", () => {
		const script = join(__dirname, "..", "..", "..", "..", "scripts", "sync-messages.mjs");
		expect(() => execFileSync("node", [script, "--check"], { stdio: "pipe" })).not.toThrow();
	});
});
