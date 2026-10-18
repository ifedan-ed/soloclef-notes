"use client";

import { Check, Copy, ExternalLink, Radio, Trash2 } from "lucide-react";
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
import { liveNotebookUrl } from "@/features/public-sharing/lib/public-link-url";
import type { LiveLink } from "@/features/public-sharing/model/live-link";
import { copyToClipboard } from "@/lib/utils";

/** One live notebook link on the Public links page: what, who, until when, copy and revoke. */
export function LiveNotebookLinkRow({
	link,
	onRevoke,
	revoking,
}: {
	link: LiveLink;
	onRevoke: (link: LiveLink) => Promise<void>;
	revoking: boolean;
}) {
	const t = useTranslations("publicSharing.links");
	const locale = useLocale();
	const [copied, setCopied] = useState(false);
	const url = liveNotebookUrl(
		typeof window === "undefined" ? "" : window.location.origin,
		link.share_token
	);
	const date = (value: string) =>
		new Date(value).toLocaleDateString(locale, { year: "numeric", month: "short", day: "numeric" });
	const title = `${link.notebook_icon ? `${link.notebook_icon} ` : ""}${link.notebook_title}`;

	const copy = async () => {
		if (!(await copyToClipboard(url))) return;
		setCopied(true);
		window.setTimeout(() => setCopied(false), 2000);
	};

	return (
		<li
			className="flex flex-col gap-3 rounded-lg border bg-accent/20 p-4 sm:flex-row sm:items-center"
			data-testid="public-link-row"
			data-kind="notebook"
		>
			<div className="min-w-0 flex-1 space-y-1">
				<div className="flex min-w-0 items-center gap-2">
					<Radio className="size-4 shrink-0 text-muted-foreground" aria-hidden />
					<h4 className="truncate text-sm font-semibold" title={title}>
						{title}
					</h4>
					<Badge variant="secondary" className="shrink-0 text-[10px]">
						{t("kind_notebook")}
					</Badge>
					{link.is_expired ? (
						<Badge variant="destructive" className="shrink-0 text-[10px]">
							{t("expired")}
						</Badge>
					) : null}
				</div>
				<p className="text-xs text-muted-foreground">
					{link.created_by_name
						? t("created_by", { name: link.created_by_name, date: date(link.created_at) })
						: t("created", { date: date(link.created_at) })}
					{" · "}
					{link.expires_at
						? link.is_expired
							? t("expired_on", { date: date(link.expires_at) })
							: t("expires", { date: date(link.expires_at) })
						: t("never")}
				</p>
			</div>
			<div className="flex shrink-0 items-center gap-1">
				<Button variant="outline" size="sm" onClick={() => void copy()} disabled={link.is_expired}>
					{copied ? <Check className="size-4" /> : <Copy className="size-4" />}
					{copied ? t("copied") : t("copy")}
				</Button>
				{link.is_expired ? null : (
					<Button variant="ghost" size="icon" className="size-8" asChild>
						<a href={url} target="_blank" rel="noopener noreferrer" aria-label={t("open")}>
							<ExternalLink className="size-4" />
						</a>
					</Button>
				)}
				{link.can_revoke ? (
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
									{t("revoke_body", { title: link.notebook_title })}
								</AlertDialogDescription>
							</AlertDialogHeader>
							<AlertDialogFooter>
								<AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
								<AlertDialogAction
									onClick={() => void onRevoke(link)}
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
