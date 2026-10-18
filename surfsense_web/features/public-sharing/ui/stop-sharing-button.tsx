"use client";

import { Link2Off } from "lucide-react";
import { useTranslations } from "next-intl";
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
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { publicLinksApi } from "../api/public-links-api";

/** Confirm "Stop sharing" in the page; the link stops opening for everyone at once. */
export function StopSharingDialog({
	open,
	onOpenChange,
	workspaceId,
	notebookId,
	onStopped,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	workspaceId: number;
	notebookId: number;
	onStopped: () => void;
}) {
	const t = useTranslations("publicSharing.notebook");
	const tl = useTranslations("publicSharing.links");
	const [stopping, setStopping] = useState(false);

	const stop = async () => {
		setStopping(true);
		try {
			await publicLinksApi.stopSharingNotebook(workspaceId, notebookId);
			toast.success(t("stopped"));
			onOpenChange(false);
			onStopped();
		} catch {
			toast.error(t("stop_failed"));
		} finally {
			setStopping(false);
		}
	};

	return (
		<AlertDialog open={open} onOpenChange={onOpenChange}>
			<AlertDialogContent>
				<AlertDialogHeader>
					<AlertDialogTitle>{t("stop_title")}</AlertDialogTitle>
					<AlertDialogDescription>{t("stop_body")}</AlertDialogDescription>
				</AlertDialogHeader>
				<AlertDialogFooter>
					<AlertDialogCancel>{tl("cancel")}</AlertDialogCancel>
					<AlertDialogAction
						onClick={(event) => {
							event.preventDefault();
							void stop();
						}}
						disabled={stopping}
						className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
						data-testid="notebook-stop-sharing-confirm"
					>
						{t("stop")}
					</AlertDialogAction>
				</AlertDialogFooter>
			</AlertDialogContent>
		</AlertDialog>
	);
}

export function StopSharingButton({
	workspaceId,
	notebookId,
	onStopped,
}: {
	workspaceId: number;
	notebookId: number;
	onStopped: () => void;
}) {
	const t = useTranslations("publicSharing.notebook");
	const [open, setOpen] = useState(false);
	return (
		<>
			<Button
				type="button"
				variant="ghost"
				size="sm"
				className="text-destructive hover:text-destructive"
				onClick={() => setOpen(true)}
				data-testid="notebook-stop-sharing"
			>
				<Link2Off className="size-4" />
				{t("stop")}
			</Button>
			<StopSharingDialog
				open={open}
				onOpenChange={setOpen}
				workspaceId={workspaceId}
				notebookId={notebookId}
				onStopped={onStopped}
			/>
		</>
	);
}
