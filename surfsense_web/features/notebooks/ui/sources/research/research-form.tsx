"use client";

import { ArrowRight, Link2, Search } from "lucide-react";
import { useTranslations } from "next-intl";
import type { Ref } from "react";
import { Button } from "@/components/ui/button";
import { initialSourcePicks, type SourcePicks } from "../../../lib/research-source-picks";
import type { ResearchMode } from "../../../model/research";
import {
	isPageUrl,
	type PageReader,
	READ_PAGE_ID,
	type UnavailableSources,
} from "../../../model/research-sources";
import { ResearchModeMenu } from "./research-mode-menu";
import { ResearchSourceMenu } from "./research-source-menu";

export interface ResearchDraft {
	/** The search query, or the pasted link when reading a page. */
	query: string;
	/** Where to look: one source, several at once, or "Read a web page". */
	picks: SourcePicks;
	mode: ResearchMode;
}

export const initialResearchDraft: ResearchDraft = {
	query: "",
	picks: initialSourcePicks,
	mode: "fast",
};

/**
 * The "Search the web for new sources" box: a query, where to look, and how
 * hard; or, with "Read a web page" picked, one link to add as a source.
 */
export function ResearchForm({
	draft,
	onDraftChange,
	onSubmit,
	inputRef,
	unavailable,
	pageReader,
	busy = false,
}: {
	draft: ResearchDraft;
	onDraftChange: (draft: ResearchDraft) => void;
	onSubmit: () => void;
	inputRef?: Ref<HTMLInputElement>;
	/** Sources greyed out in the menu, e.g. Exa with no key in this workspace. */
	unavailable?: UnavailableSources;
	/** What reads a pasted page in this workspace. */
	pageReader?: PageReader;
	/** A page is being added: the button waits. */
	busy?: boolean;
}) {
	const t = useTranslations("notebook_research");
	const readingPage = draft.picks.source === READ_PAGE_ID;
	const canSubmit = !busy && (readingPage ? isPageUrl(draft.query) : draft.query.trim().length > 0);
	const placeholder = readingPage ? t("page_placeholder") : t("search_placeholder");
	const Icon = readingPage ? Link2 : Search;
	return (
		<form
			onSubmit={(event) => {
				event.preventDefault();
				if (canSubmit) onSubmit();
			}}
			className="rounded-2xl border bg-background p-2 transition-colors focus-within:border-ring"
		>
			<div className="flex items-center gap-2 px-1">
				<Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden />
				<input
					ref={inputRef}
					value={draft.query}
					onChange={(event) => onDraftChange({ ...draft, query: event.target.value })}
					placeholder={placeholder}
					aria-label={placeholder}
					type={readingPage ? "url" : "text"}
					inputMode={readingPage ? "url" : undefined}
					enterKeyHint={readingPage ? "go" : "search"}
					maxLength={readingPage ? 2048 : 500}
					className="h-8 min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
				/>
			</div>
			<div className="mt-1 flex min-w-0 items-center gap-1.5">
				<ResearchSourceMenu
					picks={draft.picks}
					unavailable={unavailable}
					pageReader={pageReader}
					onChange={(picks) => onDraftChange({ ...draft, picks })}
				/>
				{readingPage ? null : (
					<ResearchModeMenu
						value={draft.mode}
						onChange={(mode) => onDraftChange({ ...draft, mode })}
					/>
				)}
				<span className="flex-1" />
				<Button
					type="submit"
					size="icon"
					className="size-7 shrink-0 rounded-full"
					disabled={!canSubmit}
					aria-label={readingPage ? t("page_submit") : t("submit")}
				>
					<ArrowRight className="size-4" />
				</Button>
			</div>
		</form>
	);
}
