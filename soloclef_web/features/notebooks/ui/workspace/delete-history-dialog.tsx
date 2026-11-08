"use client";

import { useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";

/** Confirm deleting the caller's own chats in this notebook. */
export function DeleteHistoryDialog({
	open,
	onOpenChange,
	onConfirm,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onConfirm: () => Promise<unknown>;
}) {
	const t = useTranslations("notebooks");
	const [busy, setBusy] = useState(false);
	const [failed, setFailed] = useState(false);
	const showBusy = useDelayedFlag(busy);

	const confirm = async () => {
		setBusy(true);
		setFailed(false);
		try {
			await onConfirm();
			onOpenChange(false);
		} catch {
			setFailed(true);
		} finally {
			setBusy(false);
		}
	};

	return (
		<Dialog
			open={open}
			onOpenChange={(next) => {
				if (!busy) {
					setFailed(false);
					onOpenChange(next);
				}
			}}
		>
			<DialogContent className="w-[calc(100vw-2rem)] max-w-md">
				<DialogHeader>
					<DialogTitle>{t("delete_history.title")}</DialogTitle>
					<DialogDescription>{t("delete_history.body")}</DialogDescription>
				</DialogHeader>
				{failed ? (
					<p className="text-sm text-destructive" role="alert">
						{t("delete_history.failed")}
					</p>
				) : null}
				<div className="flex justify-end gap-2">
					<Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
						{t("cancel")}
					</Button>
					<Button variant="destructive" onClick={confirm} disabled={busy}>
						{showBusy ? t("deleting") : t("delete_history.confirm")}
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}
