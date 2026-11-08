"use client";

import { useState } from "react";
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
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { SentAnnouncement } from "../api/announcements-admin-api";
import { useWithdrawAnnouncement } from "../hooks/use-announcements-admin";
import { plainTextOf } from "../lib/markdown-lite";

const STATUS_LABEL: Record<SentAnnouncement["status"], string> = {
	live: "Live",
	expired: "Expired",
	withdrawn: "Withdrawn",
};

function when(iso: string): string {
	return new Date(iso).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

/** What has been sent, to whom, how many have read it, and a way to take it back. */
export function SentAnnouncementsList({ items }: { items: SentAnnouncement[] }) {
	const [withdrawing, setWithdrawing] = useState<SentAnnouncement | null>(null);
	const withdraw = useWithdrawAnnouncement(() => setWithdrawing(null));

	if (items.length === 0) {
		return <p className="text-sm text-muted-foreground">Nothing sent yet.</p>;
	}
	return (
		<>
			<ul className="divide-y rounded-lg border">
				{items.map((item) => (
					<li key={item.id} className="flex flex-wrap items-start justify-between gap-2 p-3">
						<div className="min-w-0 flex-1">
							<p className="flex flex-wrap items-center gap-2 font-medium">
								{item.title}
								<Badge variant={item.status === "live" ? "secondary" : "outline"}>
									{STATUS_LABEL[item.status]}
								</Badge>
							</p>
							<p className="line-clamp-1 text-sm text-muted-foreground">{plainTextOf(item.body)}</p>
							<p className="mt-1 text-xs text-muted-foreground">
								{item.audience === "all"
									? "Everyone"
									: `Workspace: ${item.workspace_name ?? `#${item.workspace_id}`}`}
								{" · "}
								{when(item.created_at)}
								{" · "}
								{item.status === "withdrawn"
									? `${item.recipient_count} recipients`
									: `read by ${item.read_count} of ${item.recipient_count}`}
								{item.expires_at ? ` · expires ${when(item.expires_at)}` : ""}
							</p>
						</div>
						{item.status !== "withdrawn" ? (
							<Button variant="outline" size="sm" onClick={() => setWithdrawing(item)}>
								Withdraw
							</Button>
						) : null}
					</li>
				))}
			</ul>
			<AlertDialog
				open={withdrawing !== null}
				onOpenChange={(open) => !open && setWithdrawing(null)}
			>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Withdraw “{withdrawing?.title}”?</AlertDialogTitle>
						<AlertDialogDescription>
							It disappears from every recipient's Notifications, read or not. The record stays
							here.
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>Cancel</AlertDialogCancel>
						<AlertDialogAction
							disabled={withdraw.isPending}
							onClick={(event) => {
								event.preventDefault();
								if (withdrawing) withdraw.mutate(withdrawing.id);
							}}
						>
							Withdraw
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</>
	);
}
