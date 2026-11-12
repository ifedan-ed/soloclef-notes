"use client";
import { useAtomValue } from "jotai";
import { MoreHorizontal, Pause, Play, Trash2 } from "lucide-react";
import { useState } from "react";
import { updateAutomationMutationAtom } from "@/atoms/automations/automations-mutation.atoms";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { AutomationSummary } from "@/contracts/types/automation.types";
import { DeleteAutomationDialog } from "./delete-automation-dialog";

interface AutomationRowActionsProps {
	automation: AutomationSummary;
}

/**
 * Three-dot menu on each row: pause/resume (not for an archived
 * automation) and delete.
 */
export function AutomationRowActions({ automation }: AutomationRowActionsProps) {
	const { mutateAsync: updateAutomation, isPending: updating } = useAtomValue(
		updateAutomationMutationAtom
	);
	const [deleteOpen, setDeleteOpen] = useState(false);

	const nextStatus = automation.status === "active" ? "paused" : "active";
	const pauseLabel = automation.status === "active" ? "Pause" : "Resume";
	const PauseIcon = automation.status === "active" ? Pause : Play;
	const canToggle = automation.status !== "archived";

	async function handleTogglePause() {
		await updateAutomation({
			automationId: automation.id,
			patch: { status: nextStatus },
		});
	}

	return (
		<>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button
						variant="ghost"
						size="icon"
						className="h-6 w-6 hover:bg-transparent"
						aria-label={`Actions for ${automation.name}`}
					>
						<MoreHorizontal className="h-3.5 w-3.5 text-muted-foreground" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end" className="w-32 z-80">
					{canToggle && (
						<DropdownMenuItem onSelect={handleTogglePause} disabled={updating}>
							<PauseIcon className="mr-2 h-4 w-4" />
							{pauseLabel}
						</DropdownMenuItem>
					)}
					<DropdownMenuItem onSelect={() => setDeleteOpen(true)}>
						<Trash2 className="mr-2 h-4 w-4" />
						Delete
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>

			<DeleteAutomationDialog
				open={deleteOpen}
				onOpenChange={setDeleteOpen}
				automationId={automation.id}
				automationName={automation.name}
			/>
		</>
	);
}
