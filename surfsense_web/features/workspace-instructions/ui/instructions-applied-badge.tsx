"use client";

import { useQuery } from "@tanstack/react-query";
import { BookText } from "lucide-react";
import Link from "next/link";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { workspacesApiService } from "@/lib/apis/workspaces-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { instructionsApplied } from "../lib/instructions";

/**
 * A small mark in the chat composer when the workspace has System
 * Instructions, so people know its answers are shaped by them; it links to
 * the settings page that shows them. Shares the
 * settings page's query, so a save shows or hides it at once.
 */
export function InstructionsAppliedBadge({
	workspaceId,
	compact = false,
}: {
	workspaceId: number | null | undefined;
	compact?: boolean;
}) {
	const { data: workspace } = useQuery({
		queryKey: cacheKeys.workspaces.detail(String(workspaceId ?? "")),
		queryFn: () => workspacesApiService.getWorkspace({ id: Number(workspaceId) }),
		enabled: !!workspaceId,
		staleTime: 60_000,
	});
	if (!instructionsApplied(workspace?.qna_custom_instructions)) return null;
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Link
					href={`/dashboard/${workspaceId}/workspace-settings/prompts`}
					data-testid="workspace-instructions-applied"
					aria-label="Workspace instructions applied"
					className="inline-flex h-9 shrink-0 items-center gap-1.5 rounded-full border border-primary/10 bg-secondary/50 px-3 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
				>
					<BookText className="size-3.5" aria-hidden />
					{compact ? null : "Instructions applied"}
				</Link>
			</TooltipTrigger>
			<TooltipContent side="top" className="max-w-64">
				Workspace instructions applied: answers here follow the workspace owner's style guidance
				(tone, audience, format). They still come only from your sources, with citations.
			</TooltipContent>
		</Tooltip>
	);
}
