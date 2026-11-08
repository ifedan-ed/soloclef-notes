"use client";

import { MarkdownViewer } from "@/components/markdown-viewer";
import type { ReportOutline } from "../../lib/report-outline";
import { suggestionPhase } from "../../lib/report-suggestions";
import type { ReportSuggestion, StudioOutput } from "../../model/notebook";
import { ReportEmbedCard } from "./report-embed";
import { ReportSuggestionCard } from "./report-suggestion-card";

export const sectionAnchor = (index: number) => `report-section-${index + 1}`;

/** A Markdown report as a reading document, suggestions under their sections. */
export function ReportDocument({
	outline,
	fallbackTitle,
	placed,
	outputs,
	workspaceId,
	adding,
	failedToStart,
	onAdd,
	onDismiss,
}: {
	outline: ReportOutline;
	fallbackTitle: string;
	placed: Map<number, ReportSuggestion[]>;
	outputs: ReadonlyMap<number, StudioOutput>;
	workspaceId: number;
	adding: string | null;
	failedToStart: ReadonlySet<string>;
	onAdd: (suggestion: ReportSuggestion) => void;
	onDismiss: (suggestion: ReportSuggestion) => void;
}) {
	return (
		<article className="mx-auto w-full min-w-0 max-w-3xl px-4 pb-[30vh] pt-6 sm:px-8">
			<h1 className="mb-4 break-words text-2xl font-semibold tracking-tight sm:text-3xl">
				{outline.title ?? fallbackTitle}
			</h1>
			{outline.preamble ? <MarkdownViewer content={outline.preamble} /> : null}
			{outline.sections.map((section) => (
				<section
					key={section.index}
					id={sectionAnchor(section.index)}
					data-report-section={section.index}
					tabIndex={-1}
					className="scroll-mt-4 outline-none"
				>
					<MarkdownViewer content={section.markdown} />
					{(placed.get(section.index) ?? []).map((suggestion) => {
						const output =
							suggestion.output_id != null ? outputs.get(suggestion.output_id) : undefined;
						const phase = suggestionPhase(suggestion, output, adding === suggestion.id);
						if (phase === "ready" && output) {
							return (
								<ReportEmbedCard
									key={suggestion.id}
									suggestionId={suggestion.id}
									output={output}
									workspaceId={workspaceId}
								/>
							);
						}
						return (
							<ReportSuggestionCard
								key={suggestion.id}
								suggestion={suggestion}
								phase={phase === "ready" ? "failed" : phase}
								output={output}
								error={failedToStart.has(suggestion.id)}
								onAdd={() => onAdd(suggestion)}
								onDismiss={() => onDismiss(suggestion)}
							/>
						);
					})}
				</section>
			))}
		</article>
	);
}
