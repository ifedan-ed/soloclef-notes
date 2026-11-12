"use client";

import { BookText } from "lucide-react";
import Link from "next/link";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useAccountSettings } from "@/features/account-settings/hooks/use-account-settings";
import { settingsPath } from "@/lib/dashboard-paths";
import { instructionsApplied } from "../lib/instructions";

/**
 * A small mark in the chat composer when the account has chat instructions,
 * so the person remembers their answers are shaped by them; it links to the
 * settings section that shows them. It shares that section's query, so a
 * save shows or hides it at once.
 */
export function InstructionsAppliedBadge() {
	const { data: settings } = useAccountSettings();
	if (!instructionsApplied(settings?.chat_instructions)) return null;
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Link
					href={settingsPath("instructions")}
					data-testid="chat-instructions-applied"
					aria-label="Your instructions applied"
					className="inline-flex h-9 shrink-0 items-center gap-1.5 rounded-full border border-primary/10 bg-secondary/50 px-3 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
				>
					<BookText className="size-3.5" aria-hidden />
					Instructions applied
				</Link>
			</TooltipTrigger>
			<TooltipContent side="top" className="max-w-64">
				Your instructions apply: answers follow the style you set (tone, audience, format). They
				still come only from your sources, with citations.
			</TooltipContent>
		</Tooltip>
	);
}
