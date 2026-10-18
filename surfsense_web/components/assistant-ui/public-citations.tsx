"use client";

import { FileText } from "lucide-react";
import { createContext, type FC, type ReactNode, useContext } from "react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";

/** What a numbered citation on a public page points at: a source the visitor may open. */
export interface PublicCitationTarget {
	/** Shown in the tooltip: the source's title, and page when known. */
	label: string;
	open: () => void;
}

type Resolve = (number: number) => PublicCitationTarget | null;

const PublicCitationsContext = createContext<Resolve | null>(null);

/**
 * On a public page citations are numbered by the server and resolve here, to
 * the notebook's own sources; nothing is fetched with a session.
 */
export const PublicCitationsProvider: FC<{ resolve: Resolve; children: ReactNode }> = ({
	resolve,
	children,
}) => <PublicCitationsContext.Provider value={resolve}>{children}</PublicCitationsContext.Provider>;

export function usePublicCitations(): Resolve | null {
	return useContext(PublicCitationsContext);
}

export const PublicCitation: FC<{ number: number; target: PublicCitationTarget | null }> = ({
	number,
	target,
}) => {
	const pill =
		"ml-0.5 inline-flex h-5 min-w-5 items-center justify-center gap-0.5 rounded-md bg-popover px-1.5 text-[11px] font-medium text-popover-foreground/80 align-baseline";
	if (!target) {
		return (
			<span className={pill} role="note">
				<FileText className="size-3" />
				{number}
			</span>
		);
	}
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					type="button"
					onClick={target.open}
					className={`${pill} hover:bg-accent hover:text-accent-foreground`}
					aria-label={target.label}
					data-testid="public-citation"
				>
					{number}
				</button>
			</TooltipTrigger>
			<TooltipContent>{target.label}</TooltipContent>
		</Tooltip>
	);
};
