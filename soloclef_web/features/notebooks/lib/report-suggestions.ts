import type { ReportSuggestion, StudioOutput } from "../model/notebook";
import type { ReportSection } from "./report-outline";

const norm = (text: string) => text.trim().toLocaleLowerCase().replace(/\s+/g, " ");

/**
 * Which section each shown suggestion sits under, by section index.
 *
 * The backend stores the section's index and heading from the same split;
 * the heading wins when the two disagree (a report whose Markdown changed),
 * a suggestion whose section no longer exists goes after the last section,
 * and dismissed ones are not shown.
 */
export function placeSuggestions(
	sections: readonly ReportSection[],
	suggestions: readonly ReportSuggestion[]
): Map<number, ReportSuggestion[]> {
	const placed = new Map<number, ReportSuggestion[]>();
	if (!sections.length) return placed;
	for (const suggestion of suggestions) {
		if (suggestion.state === "dismissed") continue;
		const heading = norm(suggestion.section_heading);
		const atIndex = sections[suggestion.section_index];
		let index: number;
		if (atIndex && (!heading || norm(atIndex.heading) === heading)) {
			index = atIndex.index;
		} else {
			const byHeading = heading ? sections.find((s) => norm(s.heading) === heading) : undefined;
			index = byHeading?.index ?? (atIndex ? atIndex.index : sections.length - 1);
		}
		placed.set(index, [...(placed.get(index) ?? []), suggestion]);
	}
	return placed;
}

export type SuggestionPhase = "open" | "starting" | "running" | "ready" | "failed";

/**
 * What a suggestion card shows: the offer, a progress card while its output
 * is made (from the Studio list, which polls), the output itself once ready,
 * or a failure with a retry.
 */
export function suggestionPhase(
	suggestion: ReportSuggestion,
	output: StudioOutput | undefined,
	adding: boolean
): SuggestionPhase {
	if (adding) return "starting";
	if (suggestion.state !== "added") return "open";
	// Just added: the Studio list has not refetched the new output yet.
	if (!output) return "starting";
	if (output.status === "ready") return output.artifact_id ? "ready" : "failed";
	if (output.status === "failed" || output.status === "cancelled") return "failed";
	return "running";
}
