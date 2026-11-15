"use client";

import { Check, Copy, ExternalLink, FileText, Trash2 } from "lucide-react";
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
import { publicLinkUrl } from "../lib/public-link-url";
import type { PublicLink } from "../model/public-link";

/** One shared Studio output: its title, when it ends, copy and revoke. */
export function PublicLinkRow({
	link,
	onRevoke,
	revoking,
}: {
	link: PublicLink;
	onRevoke: (link: PublicLink) => Promise<void>;
	revoking: boolean;
}) {
	const t = useTranslations("publicSharing.links");
	const locale = useLocale();
	const [copied, setCopied] = useState(false);
	const url = publicLinkUrl(
		typeof window === "undefined" ? "" : window.location.origin,
		link.share_token
	);
	const date = (value: string) =>
		new Date(value).toLocaleDateString(locale, { year: "numeric", month: "short", day: "numeric" });

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
					<FileText className="size-4 shrink-0 text-muted-foreground" aria-hidden />
					<h4 className="truncate text-sm font-semibold" title={link.thread_title}>
						{link.thread_title}
					</h4>
					{link.is_expired ? (
						<Badge variant="destructive" className="shrink-0 text-[10px]">
							{t("expired")}
						</Badge>
					) : null}
				</div>
				<p className="text-xs text-muted-foreground">
					{t("created", { date: date(link.created_at) })}
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
								{t("revoke_body", { title: link.thread_title })}
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
			</div>
		</li>
	);
}
