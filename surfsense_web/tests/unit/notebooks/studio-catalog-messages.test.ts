import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { STUDIO_TILES } from "@/features/notebooks/lib/studio-catalog";

const LOCALES = ["en", "es", "pt", "hi", "ko", "zh"];

function lookup(messages: Record<string, unknown>, path: string): unknown {
	return path
		.split(".")
		.reduce<unknown>(
			(node, key) =>
				node && typeof node === "object" ? (node as Record<string, unknown>)[key] : undefined,
			messages
		);
}

test("every studio tile, field and option has its strings in every locale", () => {
	for (const locale of LOCALES) {
		const messages = JSON.parse(
			readFileSync(join(process.cwd(), "messages", `${locale}.json`), "utf8")
		).notebooks;
		for (const tile of STUDIO_TILES) {
			for (const key of [
				"label",
				"tooltip",
				"dialog_title",
				"description",
				"focus_label",
				"focus_placeholder",
			]) {
				const path = `kinds.${tile.kind}.${key}`;
				assert.equal(typeof lookup(messages, path), "string", `${locale}: ${path}`);
			}
			for (const group of tile.groups) {
				assert.equal(typeof lookup(messages, `fields.${group.field}`), "string");
				for (const value of group.values) {
					const base = `options.${tile.kind}.${group.field}.${value}`;
					assert.equal(typeof lookup(messages, `${base}.label`), "string", `${locale}: ${base}`);
					if (group.presentation !== "segments") {
						assert.equal(
							typeof lookup(messages, `${base}.description`),
							"string",
							`${locale}: ${base}.description`
						);
					}
				}
			}
		}
	}
});
