"use client";
import { useAtomValue } from "jotai";
import { useState } from "react";
import { cancelRunMutationAtom } from "@/atoms/automations/cancel-run-mutation.atom";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";

interface CancelRunButtonProps {
	automationId: number;
	runId: number;
}

/**
 * "Cancel" on a run that is waiting or running, behind a confirm. The run
 * turns Cancelled at once; its worker stops within a few seconds
 * (``surfsense_backend/app/automations/runtime/cancellation.py``).
 */
export function CancelRunButton({ automationId, runId }: CancelRunButtonProps) {
	const { mutateAsync: cancelRun, isPending } = useAtomValue(cancelRunMutationAtom);
	const [open, setOpen] = useState(false);

	async function handleConfirm() {
		try {
			await cancelRun({ automationId, runId });
		} finally {
			setOpen(false);
		}
	}

	return (
		<>
			<Button
				type="button"
				variant="ghost"
				size="sm"
				onClick={() => setOpen(true)}
				disabled={isPending}
				className="h-7 shrink-0 px-2 text-xs text-muted-foreground hover:text-foreground"
			>
				Cancel
			</Button>
			<AlertDialog open={open} onOpenChange={setOpen}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Cancel this run?</AlertDialogTitle>
						<AlertDialogDescription>
							It stops within a few seconds. Anything it already saved stays.
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel disabled={isPending}>Keep running</AlertDialogCancel>
						<AlertDialogAction
							onClick={handleConfirm}
							disabled={isPending}
							className="bg-destructive text-white hover:bg-destructive/90"
						>
							{isPending ? (
								<span className="inline-flex items-center gap-2">
									<Spinner size="xs" />
									Cancelling…
								</span>
							) : (
								"Cancel run"
							)}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</>
	);
}
