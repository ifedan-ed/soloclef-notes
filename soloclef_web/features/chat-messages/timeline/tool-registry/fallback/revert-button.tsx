"use client";

import { useQueryClient } from "@tanstack/react-query";
import { RotateCcw } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
	AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { getToolDisplayName } from "@/contracts/enums/toolIcons";
import { markActionRevertedInCache } from "@/hooks/use-agent-actions-query";
import { agentActionsApiService } from "@/lib/apis/agent-actions-api.service";
import { AppError } from "@/lib/error";
import { useToolAction } from "./use-tool-action";

/**
 * Inline undo button for one change the assistant made, drawn on a
 * default-fallback tool card (``variant="card"``) or at the end of the
 * change's line in the turn's trace (``variant="trace"``), when the
 * matching ``AgentActionLog`` row is reversible and hasn't been undone yet.
 *
 * Renders ``null`` (silent) in any of these cases:
 *  - no matching action row (still streaming, or never logged)
 *  - action not reversible
 *  - already reverted (``reverted_by_action_id`` set)
 *  - this card IS itself a revert action
 *  - tool errored
 *  - no thread context
 *
 * 503 from the revert API means the deployment has revert gated off;
 * we hide the failure silently rather than nag the user. Other errors
 * surface as toasts.
 */
export function ToolCardRevertButton({
	toolCallId,
	toolName,
	langchainToolCallId,
	variant = "card",
}: {
	toolCallId: string;
	toolName: string;
	langchainToolCallId?: string;
	variant?: "card" | "trace";
}) {
	const queryClient = useQueryClient();
	const { threadId, action } = useToolAction({
		toolCallId,
		toolName,
		langchainToolCallId,
	});

	const [isReverting, setIsReverting] = useState(false);
	const [confirmOpen, setConfirmOpen] = useState(false);

	if (!action) return null;
	if (!action.reversible) return null;
	if (action.reverted_by_action_id !== null && action.reverted_by_action_id !== undefined) {
		// On the trace the line keeps saying what happened to it, after a
		// reload too: the log row carries the undo.
		return variant === "trace" ? (
			<span className="shrink-0 px-2 text-xs text-muted-foreground">Undone</span>
		) : null;
	}
	if (action.is_revert_action) return null;
	if (action.error !== null && action.error !== undefined) return null;
	if (!threadId) return null;

	const handleRevert = async () => {
		setIsReverting(true);
		try {
			const response = await agentActionsApiService.revert(threadId, action.id);
			markActionRevertedInCache(queryClient, threadId, action.id, response.new_action_id ?? null);
			toast.success(response.message || "Undone.");
		} catch (err) {
			if (err instanceof AppError && err.status === 503) {
				return;
			}
			const message =
				err instanceof AppError
					? err.message
					: err instanceof Error
						? err.message
						: "The change could not be undone.";
			toast.error(message);
		} finally {
			setIsReverting(false);
			setConfirmOpen(false);
		}
	};

	return (
		<AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
			<AlertDialogTrigger asChild>
				<Button
					size="sm"
					variant={variant === "trace" ? "ghost" : "outline"}
					className={
						variant === "trace"
							? "-my-0.5 h-6 gap-1 px-2 text-xs font-normal text-muted-foreground hover:text-foreground max-md:-my-3 max-md:min-h-11"
							: "gap-1.5"
					}
					onClick={(e) => {
						e.stopPropagation();
						setConfirmOpen(true);
					}}
					disabled={isReverting}
					aria-label={`Undo: ${getToolDisplayName(action.tool_name)}`}
				>
					{isReverting ? <Spinner size="xs" /> : <RotateCcw data-icon="inline-start" />}
					Undo
				</Button>
			</AlertDialogTrigger>
			<AlertDialogContent>
				<AlertDialogHeader>
					<AlertDialogTitle>Undo this change?</AlertDialogTitle>
					<AlertDialogDescription>
						This takes back &ldquo;{getToolDisplayName(action.tool_name)}&rdquo; in your documents
						and adds the undo to the history. Your chat stays as it is. If the item has been changed
						since, nothing is overwritten and you are told why.
					</AlertDialogDescription>
				</AlertDialogHeader>
				<AlertDialogFooter>
					<AlertDialogCancel disabled={isReverting}>Cancel</AlertDialogCancel>
					<AlertDialogAction
						onClick={(e) => {
							e.preventDefault();
							handleRevert();
						}}
						disabled={isReverting}
						className="gap-1.5"
					>
						{isReverting && <Spinner size="xs" />}
						Undo
					</AlertDialogAction>
				</AlertDialogFooter>
			</AlertDialogContent>
		</AlertDialog>
	);
}
