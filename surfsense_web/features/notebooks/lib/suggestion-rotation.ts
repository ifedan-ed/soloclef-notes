import type { StudioKind } from "../model/notebook";

/**
 * Studio dialogs show three ideas at a time from a pool the backend caches.
 * Each open starts on the next group of three, and "More ideas" steps
 * through the pool before asking the backend for a fresh one.
 */
export const SUGGESTION_WINDOW = 3;

/** The dialog option whose value changes what a good idea is. */
const VARIANT_FIELD: Partial<Record<StudioKind, string>> = {
	audio: "format",
	video: "format",
	report: "format",
	slides: "format",
	flashcards: "difficulty",
	quiz: "difficulty",
};

export function suggestionVariant(kind: StudioKind, options: Record<string, string>): string {
	const field = VARIANT_FIELD[kind];
	return field ? (options[field] ?? "") : "";
}

function modulo(value: number, size: number): number {
	return ((value % size) + size) % size;
}

/** Three distinct ideas from the pool for ``offset``, wrapping round it. */
export function suggestionWindow(
	pool: readonly string[],
	examples: readonly string[],
	offset: number
): { suggestions: string[]; placeholder: string | null } {
	const size = pool.length;
	const suggestions =
		size >= SUGGESTION_WINDOW
			? Array.from(
					{ length: SUGGESTION_WINDOW },
					(_, i) => pool[modulo(offset * SUGGESTION_WINDOW + i, size)]
				)
			: [...pool];
	const placeholder = examples.length ? examples[modulo(offset, examples.length)] : null;
	return { suggestions, placeholder };
}

export function windowCount(poolSize: number): number {
	return Math.max(1, Math.ceil(poolSize / SUGGESTION_WINDOW));
}

export interface RotationState {
	offset: number;
	/** Groups of three shown since the pool was last generated. */
	seen: number;
}

export function startRotation(offset: number): RotationState {
	return { offset, seen: 1 };
}

/** The next group, or ``exhausted`` once every group of this pool was shown. */
export function advanceRotation(
	state: RotationState,
	poolSize: number
): { state: RotationState; exhausted: boolean } {
	if (state.seen < windowCount(poolSize)) {
		return { state: { offset: state.offset + 1, seen: state.seen + 1 }, exhausted: false };
	}
	return { state, exhausted: true };
}

const STORAGE_PREFIX = "surfsense:studio-ideas-open:";
const memoryCounters = new Map<string, number>();

type CounterStorage = Pick<Storage, "getItem" | "setItem">;

function browserStorage(): CounterStorage | null {
	try {
		return typeof window === "undefined" ? null : window.localStorage;
	} catch {
		return null;
	}
}

/**
 * The offset for this open of the dialog, and one more for the next open.
 * Remembered per browser; without storage (a private window, blocked site
 * data) it still rotates for as long as the page stays open.
 */
export function claimOpenOffset(
	key: string,
	storage: CounterStorage | null = browserStorage()
): number {
	let current = memoryCounters.get(key) ?? 0;
	try {
		const stored = Number.parseInt(storage?.getItem(STORAGE_PREFIX + key) ?? "", 10);
		if (Number.isFinite(stored) && stored >= 0) current = stored;
	} catch {
		// Unreadable storage: the in-memory counter carries on.
	}
	const next = (current + 1) % 1_000_000;
	memoryCounters.set(key, next);
	try {
		storage?.setItem(STORAGE_PREFIX + key, String(next));
	} catch {
		// Unwritable storage: same.
	}
	return current;
}
