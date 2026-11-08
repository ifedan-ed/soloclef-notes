import { BRAND } from "./index";

/**
 * Locale files never spell the product name: they say `{brand}`, and this
 * fills it in once when a locale's messages are loaded. Renaming the product
 * therefore touches brand.json only, in all six languages at once. (The phone
 * app reads the same files and passes `brand` as an ICU value instead.)
 */
export const BRAND_TOKEN = /\{brand\}/g;

const cache = new WeakMap<object, unknown>();

function fill(value: unknown, name: string): unknown {
	if (typeof value === "string") return value.replace(BRAND_TOKEN, name);
	if (Array.isArray(value)) return value.map((item) => fill(item, name));
	if (value && typeof value === "object") {
		const out: Record<string, unknown> = {};
		for (const [key, child] of Object.entries(value)) out[key] = fill(child, name);
		return out;
	}
	return value;
}

export function withBrand<T extends object>(messages: T, name: string = BRAND.name): T {
	if (name === BRAND.name) {
		const hit = cache.get(messages);
		if (hit) return hit as T;
		const filled = fill(messages, name) as T;
		cache.set(messages, filled);
		return filled;
	}
	return fill(messages, name) as T;
}
