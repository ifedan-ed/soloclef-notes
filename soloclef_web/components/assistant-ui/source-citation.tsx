"use client";

import { Mail, Plug } from "lucide-react";
import type { FC, ReactNode } from "react";
import { useId } from "react";
import { PILL } from "@/components/assistant-ui/inline-citation";
import { useCitationNumber } from "@/components/citations/citation-numbers-context";
import { CitationHoverPopover } from "@/components/tool-ui/citation/citation-hover-popover";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { useMediaQuery } from "@/hooks/use-media-query";
import type { AddresslessSource, SourceCitationToken } from "@/lib/citations/addressless-source";
import { cn } from "@/lib/utils";

/** Each kind of addressless source: its icon and where it came from, in words. */
const SOURCES: Record<AddresslessSource, { Icon: typeof Mail; where: string }> = {
	mail: { Icon: Mail, where: "An email in your mailbox" },
	app: { Icon: Plug, where: "A result from a connected app" },
};

/**
 * A citation of a source with no address to open: an email the assistant
 * read in the person's mailbox, or a connected app's answer
 * (lib/citations/addressless-source.ts). It is drawn as the web citation's
 * pill with the answer's number, beside an icon that says it is mail or an
 * app, because there is nothing to link to. What it cites is shown on hover
 * on a desktop and on a tap on a touch screen, where hover does not exist:
 * the label the server wrote (sender, subject, date for mail) and where it
 * came from.
 */
export const SourceCitation: FC<{ citation: SourceCitationToken }> = ({ citation }) => {
	const reactId = useId();
	const number = useCitationNumber(citation);
	const isTouchLike = useMediaQuery("(hover: none), (pointer: coarse)");
	const { Icon, where } = SOURCES[citation.source];
	const chipClass = cn(
		PILL,
		"cursor-default hover:bg-accent hover:text-accent-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
	);
	const face: ReactNode = (
		<>
			<Icon className="size-3" aria-hidden />
			{number}
		</>
	);
	const card = (
		<div className="flex flex-col gap-1.5 p-3">
			<div className="flex items-center gap-2 text-muted-foreground text-xs">
				<Icon className="size-3.5 shrink-0" aria-hidden />
				<span>{where}</span>
			</div>
			<p className="break-words text-sm font-medium leading-snug">{citation.label}</p>
		</div>
	);
	const label = `${where}: ${citation.label}`;

	if (isTouchLike) {
		return (
			<Popover>
				<PopoverTrigger asChild>
					<button
						type="button"
						className={chipClass}
						aria-label={label}
						data-slot="source-citation"
					>
						{face}
					</button>
				</PopoverTrigger>
				<PopoverContent side="top" align="start" sideOffset={6} className="w-72 p-0">
					{card}
				</PopoverContent>
			</Popover>
		);
	}
	return (
		<CitationHoverPopover
			id={`source-cite-${reactId.replace(/:/g, "")}`}
			contentClassName="w-72 overflow-hidden p-0"
			trigger={(hoverProps) => (
				<button
					type="button"
					className={chipClass}
					aria-label={label}
					data-slot="source-citation"
					{...hoverProps}
				>
					{face}
				</button>
			)}
		>
			{card}
		</CitationHoverPopover>
	);
};
