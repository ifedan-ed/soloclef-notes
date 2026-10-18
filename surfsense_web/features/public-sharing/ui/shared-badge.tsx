"use client";

import { Check, Copy, ExternalLink, Globe, Link2Off, Settings2 } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { copyToClipboard } from "@/lib/utils";
import { liveNotebookUrl } from "../lib/public-link-url";
import type { LiveLink } from "../model/live-link";
import { StopSharingDialog } from "./stop-sharing-button";

/**
 * "Shared", beside a notebook's title while it has a live link: copy it,
 * open it, change it, or stop sharing, without going to settings.
 */
export function SharedBadge({
	workspaceId,
	notebookId,
	link,
	onSettings,
	onStopped,
}: {
	workspaceId: number;
	notebookId: number;
	link: LiveLink | null;
	/** Present for people who may change the link (Owners, by default). */
	onSettings?: () => void;
	onStopped: () => void;
}) {
	const t = useTranslations("publicSharing.notebook");
	const [open, setOpen] = useState(false);
	const [stopOpen, setStopOpen] = useState(false);
	const [copied, setCopied] = useState(false);
	if (!link || link.is_expired) return null;
	const url = liveNotebookUrl(
		typeof window === "undefined" ? "" : window.location.origin,
		link.share_token
	);

	const copy = async () => {
		if (!(await copyToClipboard(url))) return;
		setCopied(true);
		window.setTimeout(() => setCopied(false), 1500);
	};

	return (
		<>
			<Popover open={open} onOpenChange={setOpen}>
				<PopoverTrigger asChild>
					<button
						type="button"
						className="inline-flex h-7 shrink-0 items-center gap-1 rounded-full border border-emerald-300 bg-emerald-50 px-2.5 text-xs font-medium text-emerald-800 hover:bg-emerald-100 dark:border-emerald-700/60 dark:bg-emerald-950/40 dark:text-emerald-200 dark:hover:bg-emerald-900/40"
						aria-label={t("shared_hint")}
						data-testid="notebook-shared-badge"
					>
						<Globe className="size-3.5" aria-hidden />
						<span className="hidden sm:inline">{t("shared")}</span>
					</button>
				</PopoverTrigger>
				<PopoverContent align="end" className="w-64 space-y-1 p-2">
					<p className="px-2 pt-1 pb-2 text-xs text-muted-foreground">{t("shared_hint")}</p>
					<Button
						variant="ghost"
						size="sm"
						className="w-full justify-start"
						onClick={() => void copy()}
						data-testid="notebook-shared-copy"
					>
						{copied ? <Check className="size-4" /> : <Copy className="size-4" />}
						{t("copy_link")}
					</Button>
					<Button variant="ghost" size="sm" className="w-full justify-start" asChild>
						<a href={url} target="_blank" rel="noopener noreferrer">
							<ExternalLink className="size-4" />
							{t("open_link")}
						</a>
					</Button>
					{onSettings ? (
						<Button
							variant="ghost"
							size="sm"
							className="w-full justify-start"
							onClick={() => {
								setOpen(false);
								onSettings();
							}}
						>
							<Settings2 className="size-4" />
							{t("settings")}
						</Button>
					) : null}
					{link.can_revoke ? (
						<Button
							variant="ghost"
							size="sm"
							className="w-full justify-start text-destructive hover:text-destructive"
							onClick={() => {
								setOpen(false);
								setStopOpen(true);
							}}
							data-testid="notebook-shared-stop"
						>
							<Link2Off className="size-4" />
							{t("stop")}
						</Button>
					) : null}
				</PopoverContent>
			</Popover>
			<StopSharingDialog
				open={stopOpen}
				onOpenChange={setStopOpen}
				workspaceId={workspaceId}
				notebookId={notebookId}
				onStopped={onStopped}
			/>
		</>
	);
}
