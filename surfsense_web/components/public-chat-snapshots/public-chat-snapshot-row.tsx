"use client";

import { Check, Copy, ExternalLink, FileText, MessageSquare, Trash2 } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
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
	AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { PublicChatSnapshotDetail } from "@/contracts/types/chat-threads.types";
import { publicLinkUrl } from "@/features/public-sharing/lib/public-link-url";

interface PublicChatSnapshotRowProps {
	snapshot: PublicChatSnapshotDetail;
	canRevoke: boolean;
	onRevoke: (snapshot: PublicChatSnapshotDetail) => Promise<void>;
	revoking: boolean;
}

/** One public link: what it shows, who made it, when it ends, copy and revoke. */
export function PublicChatSnapshotRow({
	snapshot,
	canRevoke,
	onRevoke,
	revoking,
}: PublicChatSnapshotRowProps) {
	const t = useTranslations("publicSharing.links");
	const locale = useLocale();
	const [copied, setCopied] = useState(false);
	const url = publicLinkUrl(
		typeof window === "undefined" ? "" : window.location.origin,
		snapshot.share_token
	);
	const date = (value: string) =>
		new Date(value).toLocaleDateString(locale, { year: "numeric", month: "short", day: "numeric" });
	const isOutput = snapshot.kind === "output";
	const KindIcon = isOutput ? FileText : MessageSquare;

	const copy = async () => {
		try {
			await navigator.clipboard.writeText(url);
			setCopied(true);
			window.setTimeout(() => setCopied(false), 2000);
		} catch {
			setCopied(false);
		}
	};

	return (
		<li
			className="flex flex-col gap-3 rounded-lg border bg-accent/20 p-4 sm:flex-row sm:items-center"
			data-testid="public-link-row"
		>
			<div className="min-w-0 flex-1 space-y-1">
				<div className="flex min-w-0 items-center gap-2">
					<KindIcon className="size-4 shrink-0 text-muted-foreground" aria-hidden />
					<h4 className="truncate text-sm font-semibold" title={snapshot.thread_title}>
						{snapshot.thread_title}
					</h4>
					<Badge variant="secondary" className="shrink-0 text-[10px]">
						{isOutput ? t("kind_output") : t("kind_chat")}
					</Badge>
					{snapshot.is_expired ? (
						<Badge variant="destructive" className="shrink-0 text-[10px]">
							{t("expired")}
						</Badge>
					) : null}
				</div>
				<p className="text-xs text-muted-foreground">
					{snapshot.created_by_name
						? t("created_by", { name: snapshot.created_by_name, date: date(snapshot.created_at) })
						: t("created", { date: date(snapshot.created_at) })}
					{" · "}
					{snapshot.expires_at
						? snapshot.is_expired
							? t("expired_on", { date: date(snapshot.expires_at) })
							: t("expires", { date: date(snapshot.expires_at) })
						: t("never")}
				</p>
			</div>
			<div className="flex shrink-0 items-center gap-1">
				<Button
					variant="outline"
					size="sm"
					onClick={() => void copy()}
					disabled={snapshot.is_expired}
				>
					{copied ? <Check className="size-4" /> : <Copy className="size-4" />}
					{copied ? t("copied") : t("copy")}
				</Button>
				{snapshot.is_expired ? null : (
					<Button variant="ghost" size="icon" className="size-8" asChild>
						<a href={url} target="_blank" rel="noopener noreferrer" aria-label={t("open")}>
							<ExternalLink className="size-4" />
						</a>
					</Button>
				)}
				{canRevoke ? (
					<AlertDialog>
						<AlertDialogTrigger asChild>
							<Button
								variant="ghost"
								size="icon"
								className="size-8 text-muted-foreground hover:text-destructive"
								aria-label={t("revoke")}
								disabled={revoking}
							>
								<Trash2 className="size-4" />
							</Button>
						</AlertDialogTrigger>
						<AlertDialogContent>
							<AlertDialogHeader>
								<AlertDialogTitle>{t("revoke_title")}</AlertDialogTitle>
								<AlertDialogDescription>
									{t("revoke_body", { title: snapshot.thread_title })}
								</AlertDialogDescription>
							</AlertDialogHeader>
							<AlertDialogFooter>
								<AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
								<AlertDialogAction
									onClick={() => void onRevoke(snapshot)}
									className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
								>
									{t("revoke")}
								</AlertDialogAction>
							</AlertDialogFooter>
						</AlertDialogContent>
					</AlertDialog>
				) : null}
			</div>
		</li>
	);
}
