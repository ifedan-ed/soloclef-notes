"use client";

import type { ToolCallMessagePartProps } from "@assistant-ui/react";
import {
	AlertCircleIcon,
	CheckCircle2Icon,
	ChevronRightIcon,
	ExternalLinkIcon,
	FileQuestionIcon,
	GlobeIcon,
} from "lucide-react";
import { useState } from "react";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { cn } from "@/lib/utils";
import {
	type PageReaderCardData,
	type PageReaderPage,
	pageAddress,
	pageReaderSummary,
	parsePageReaderCard,
} from "../page-reader-card";

const STATUS_ICON = {
	success: <CheckCircle2Icon className="size-4 shrink-0 text-emerald-600" aria-label="Read" />,
	empty: <FileQuestionIcon className="size-4 shrink-0 text-amber-600" aria-label="No text" />,
	failed: <AlertCircleIcon className="size-4 shrink-0 text-destructive" aria-label="Not read" />,
} as const;

function Address({ url }: { url: string }) {
	const { host, rest } = pageAddress(url);
	return (
		<span className="flex min-w-0 flex-1 items-baseline gap-1 text-sm" title={url}>
			<span className="max-w-[70%] shrink-0 truncate">{host}</span>
			{rest ? <span className="min-w-0 truncate text-xs text-muted-foreground">{rest}</span> : null}
		</span>
	);
}

function OpenPageLink({ url }: { url: string }) {
	const { href } = pageAddress(url);
	if (!href) return null;
	return (
		<a
			href={href}
			target="_blank"
			// The page is one the model chose: it gets no handle on this window.
			rel="noopener noreferrer"
			className="inline-flex items-center gap-1 rounded-sm text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
		>
			Open page
			<ExternalLinkIcon className="size-3" aria-hidden />
		</a>
	);
}

function PageRow({ page }: { page: PageReaderPage }) {
	const [open, setOpen] = useState(false);

	// A page with no text has nothing to unfold: its reason is the row.
	if (page.status !== "success") {
		return (
			<li className="flex gap-2 px-4 py-2.5">
				<span className="mt-0.5">{STATUS_ICON[page.status]}</span>
				<div className="min-w-0 flex-1 space-y-0.5">
					<div className="flex min-w-0 items-center gap-2">
						<Address url={page.url} />
						<OpenPageLink url={page.url} />
					</div>
					<p className="break-words text-xs text-muted-foreground">
						{page.error ||
							(page.status === "empty"
								? "The page had no readable text."
								: "The page could not be read.")}
					</p>
				</div>
			</li>
		);
	}

	const hasPreview = Boolean(page.content_preview);
	return (
		<li>
			<Collapsible open={open} onOpenChange={setOpen}>
				<CollapsibleTrigger
					disabled={!hasPreview}
					className="flex w-full items-center gap-2 px-4 py-2.5 text-left transition-colors hover:bg-accent focus-visible:bg-accent focus-visible:outline-none disabled:hover:bg-transparent"
				>
					{STATUS_ICON.success}
					<Address url={page.url} />
					<ChevronRightIcon
						className={cn(
							"size-3.5 shrink-0 text-muted-foreground transition-transform",
							open && "rotate-90",
							!hasPreview && "invisible"
						)}
						aria-hidden
					/>
				</CollapsibleTrigger>
				<CollapsibleContent>
					<div className="space-y-2 bg-muted/20 px-4 py-3 pl-10">
						{/* The start of the page as the model read it: plain text, never markup. */}
						<p className="max-h-48 overflow-y-auto whitespace-pre-wrap break-words text-xs leading-relaxed text-muted-foreground">
							{page.content_preview}
						</p>
						<OpenPageLink url={page.url} />
					</div>
				</CollapsibleContent>
			</Collapsible>
		</li>
	);
}

/** The pages one page-reader call read: each address, how it fared, and how it began. */
export function PageReaderCard({ data }: { data: PageReaderCardData }) {
	const noneRead = data.succeeded === 0;
	return (
		<div className="my-4 w-full max-w-lg overflow-hidden rounded-xl border bg-card">
			<div className="flex items-center gap-2 px-4 py-2.5">
				<GlobeIcon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
				<span className="min-w-0 flex-1 truncate text-sm">{pageReaderSummary(data)}</span>
				{noneRead ? (
					<AlertCircleIcon className="size-4 shrink-0 text-destructive" aria-hidden />
				) : data.succeeded < data.total ? (
					<AlertCircleIcon className="size-4 shrink-0 text-amber-600" aria-hidden />
				) : null}
			</div>
			<ul className="divide-y border-t">
				{data.pages.map((page, index) => (
					// The same address may be asked for twice in one call.
					// biome-ignore lint/suspicious/noArrayIndexKey: the list is fixed once the call has ended
					<PageRow key={`${index}:${page.url}`} page={page} />
				))}
			</ul>
		</div>
	);
}

/**
 * The page reader's card in an answer. It draws only a finished call whose
 * result is the card payload; anything else is not a body tool at all
 * (isBodyTool) and stays in the turn's trace.
 */
export function PageReaderToolUI({
	result,
}: ToolCallMessagePartProps<{ urls?: string[] }, unknown>) {
	const data = parsePageReaderCard(result);
	return data ? <PageReaderCard data={data} /> : null;
}
