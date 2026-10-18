import type { StudioKind } from "../model/notebook";

/**
 * The two sizes of an open Studio output on a desktop, as in NotebookLM:
 * "column", the widened Studio column with the chat usable beside it, and
 * "expanded", the whole window. A phone always shows outputs full screen.
 */
export type OutputSize = "column" | "expanded";

/** Reports open expanded the first time (they have a contents list); the rest in the column. */
export function defaultOutputSize(kind: StudioKind): OutputSize {
	return kind === "report" ? "expanded" : "column";
}

/** Each output's chosen size, for this browser session (memory only). */
const chosen = new Map<number, OutputSize>();

export function outputSize(outputId: number, kind: StudioKind): OutputSize {
	return chosen.get(outputId) ?? defaultOutputSize(kind);
}

export function rememberOutputSize(outputId: number, size: OutputSize): void {
	chosen.set(outputId, size);
}

/** For tests. */
export function forgetOutputSizes(): void {
	chosen.clear();
}
