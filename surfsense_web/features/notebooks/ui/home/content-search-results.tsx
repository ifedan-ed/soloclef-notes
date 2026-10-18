"use client";

import { FileText, Folder, NotebookPen } from "lucide-react";
import Link from "next/link";
import { useLocale, useTranslations } from "next-intl";
import type { KeyboardEvent, RefObject } from "react";
import { Spinner } from "@/components/ui/spinner";
import { cn } from "@/lib/utils";
import { passageHref, snippetParts } from "../../lib/content-search";
import { relativeTime } from "../../lib/relative-time";
import type { NotebookSearchHit, SearchPassage } from "../../model/notebook";

const RESULT = "[data-search-result]";
const RESULTS = "[data-search-results]";

/** Up/Down walk the result links; Escape (or Up from the first) goes back to the search box. */
export function moveResultFocus(event: KeyboardEvent<HTMLElement>, onEscape?: () => void) {
	if (event.key === "Escape") {
		event.preventDefault();
		onEscape?.();
		return;
	}
	if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
	const container = event.currentTarget.closest(RESULTS);
	const links = Array.from(container?.querySelectorAll<HTMLElement>(RESULT) ?? []);
	if (links.length === 0) return;
	event.preventDefault();
	const index = links.indexOf(document.activeElement as HTMLElement);
	const next =
		event.key === "ArrowDown" ? Math.min(links.length - 1, index + 1) : index <= 0 ? -1 : index - 1;
	if (next < 0) onEscape?.();
	else links[next]?.focus();
}

function Snippet({ passage }: { passage: SearchPassage }) {
	return (
		<span className="mt-0.5 line-clamp-3 break-words text-sm leading-relaxed text-foreground/90 [overflow-wrap:anywhere]">
			{snippetParts(passage.snippet, passage.highlights).map((part) =>
				part.hit ? (
					<mark
						key={part.start}
						className="rounded-sm bg-amber-200/80 px-0.5 text-foreground dark:bg-amber-400/30"
					>
						{part.text}
					</mark>
				) : (
					<span key={part.start}>{part.text}</span>
				)
			)}
		</span>
	);
}

function PassageLink({
	href,
	passage,
	onKeyDown,
}: {
	href: string;
	passage: SearchPassage;
	onKeyDown: (event: KeyboardEvent<HTMLElement>) => void;
}) {
	const t = useTranslations("notebooks");
	const Icon = passage.kind === "note" ? NotebookPen : passage.folder_name ? Folder : FileText;
	const name = passage.folder_name ? `${passage.folder_name}/${passage.title}` : passage.title;
	return (
		<Link
			href={passageHref(href, passage)}
			data-search-result
			onKeyDown={onKeyDown}
			className="block min-w-0 rounded-xl px-2 py-1.5 outline-none transition-colors hover:bg-accent focus-visible:bg-accent focus-visible:ring-2 focus-visible:ring-ring"
		>
			<span className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
				<Icon className="size-3.5 shrink-0" aria-hidden />
				<span className="sr-only">
					{passage.kind === "note" ? t("search_inside_note") : t("search_inside_source")}:
				</span>
				<span className="min-w-0 truncate" title={name}>
					{name}
				</span>
			</span>
			<Snippet passage={passage} />
		</Link>
	);
}

/** The "Found inside notebooks" section under the title-filtered notebooks. */
export function ContentSearchResults({
	query,
	hits,
	settled,
	failed,
	showSpinner,
	notebookHref,
	onEscape,
	listRef,
}: {
	query: string;
	hits: NotebookSearchHit[];
	settled: boolean;
	failed: boolean;
	showSpinner: boolean;
	notebookHref: (id: number) => string;
	onEscape?: () => void;
	listRef?: RefObject<HTMLDivElement | null>;
}) {
	const t = useTranslations("notebooks");
	const locale = useLocale();
	const onKey = (event: KeyboardEvent<HTMLElement>) => moveResultFocus(event, onEscape);
	return (
		<section aria-labelledby="content-search-title" className="min-w-0 space-y-3">
			<div className="flex items-center gap-2">
				<h2 id="content-search-title" className="text-lg font-semibold">
					{t("search_inside_title")}
				</h2>
				{/* Reserved space: the spinner appearing never shifts the heading. */}
				<span className="flex size-4 items-center justify-center" aria-hidden>
					{showSpinner ? <Spinner size="sm" /> : null}
				</span>
			</div>
			<p className="sr-only" aria-live="polite">
				{settled ? t("search_inside_count", { count: hits.length }) : ""}
			</p>
			{failed ? (
				<p className="text-sm text-muted-foreground">{t("search_inside_failed")}</p>
			) : settled && hits.length === 0 ? (
				<p className="text-sm text-muted-foreground">{t("search_inside_empty", { query })}</p>
			) : null}
			{hits.length > 0 ? (
				<div
					ref={listRef}
					data-search-results
					className={cn(
						"flex min-w-0 flex-col gap-3",
						showSpinner && "opacity-70 transition-opacity"
					)}
				>
					{hits.map((hit) => (
						<article
							key={hit.id}
							aria-label={hit.title}
							className="min-w-0 rounded-2xl border bg-muted/30 p-2 sm:p-3"
						>
							<Link
								href={notebookHref(hit.id)}
								data-search-result
								onKeyDown={onKey}
								className="flex min-w-0 items-center gap-2 rounded-xl px-2 py-1.5 outline-none transition-colors hover:bg-accent focus-visible:bg-accent focus-visible:ring-2 focus-visible:ring-ring"
							>
								<span className="shrink-0 text-lg leading-none" aria-hidden>
									{hit.icon || "📓"}
								</span>
								<span className="min-w-0 flex-1 truncate font-medium">{hit.title}</span>
								<span className="shrink-0 text-xs text-muted-foreground">
									{t("search_inside_updated", { when: relativeTime(hit.updated_at, locale) })}
								</span>
							</Link>
							<ul className="mt-1 min-w-0 space-y-0.5">
								{hit.passages.map((passage) => (
									<li
										key={`${passage.kind}-${passage.note_id ?? passage.document_id}-${passage.source_id}-${passage.snippet.length}-${passage.score}`}
										className="min-w-0"
									>
										<PassageLink href={notebookHref(hit.id)} passage={passage} onKeyDown={onKey} />
									</li>
								))}
							</ul>
						</article>
					))}
				</div>
			) : null}
		</section>
	);
}
