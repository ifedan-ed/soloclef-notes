"use client";

import { AlertTriangle, Check, Copy, Globe, RefreshCw } from "lucide-react";
import Link from "next/link";
import { useLocale, useTranslations } from "next-intl";
import { useEffect, useId, useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { copyToClipboard } from "@/lib/utils";
import { publicLinksApi } from "../api/public-links-api";
import { liveNotebookUrl } from "../lib/public-link-url";
import type { LiveLink } from "../model/live-link";
import {
	DEFAULT_EXPIRY,
	EXPIRY_CHOICES,
	type ExpiryChoice,
	isExpiryChoice,
} from "../model/share-expiry";
import { StopSharingButton } from "./stop-sharing-button";

type Busy = "save" | "regenerate" | null;

/** The expiry choice that matches a link's current expiry, roughly. */
function expiryOf(link: LiveLink | null): ExpiryChoice {
	if (!link) return DEFAULT_EXPIRY;
	if (!link.expires_at) return "never";
	const days = (new Date(link.expires_at).getTime() - Date.now()) / 86_400_000;
	return days > 8 ? "30" : "7";
}

/**
 * "Share notebook…": one live, read-only link per notebook. The warning sits
 * above the button, not in a popup; once the link exists the dialog shows it,
 * with its expiry, a new address, and stop sharing.
 */
export function NotebookShareDialog({
	open,
	onOpenChange,
	workspaceId,
	notebookId,
	notebookTitle,
	link,
	onChange,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	workspaceId: number;
	notebookId: number;
	notebookTitle: string;
	link: LiveLink | null;
	onChange: (link: LiveLink | null) => void;
}) {
	const t = useTranslations("publicSharing");
	const tn = useTranslations("publicSharing.notebook");
	const locale = useLocale();
	const expiryId = useId();
	const [expiry, setExpiry] = useState<ExpiryChoice>(DEFAULT_EXPIRY);
	const [busy, setBusy] = useState<Busy>(null);
	const [error, setError] = useState<string | null>(null);
	const [notice, setNotice] = useState<string | null>(null);
	const [copied, setCopied] = useState(false);
	const showSpinner = useDelayedFlag(busy !== null);

	// Each opening starts from the link as it is.
	// biome-ignore lint/correctness/useExhaustiveDependencies: reset on open only
	useEffect(() => {
		if (!open) return;
		setExpiry(expiryOf(link));
		setBusy(null);
		setError(null);
		setNotice(null);
		setCopied(false);
	}, [open]);

	const live = link && !link.is_expired ? link : null;
	const url = live
		? liveNotebookUrl(typeof window === "undefined" ? "" : window.location.origin, live.share_token)
		: "";

	const save = async (regenerate: boolean) => {
		if (busy) return;
		setBusy(regenerate ? "regenerate" : "save");
		setError(null);
		setNotice(null);
		try {
			const saved = await publicLinksApi.shareNotebook(workspaceId, notebookId, expiry, regenerate);
			onChange(saved);
			if (live) setNotice(tn("updated"));
		} catch (failure) {
			const status = (failure as { status?: number } | null)?.status;
			setError(status === 403 ? t("not_allowed") : t("create_failed"));
		} finally {
			setBusy(null);
		}
	};

	const copy = async () => {
		if (await copyToClipboard(url)) {
			setCopied(true);
			window.setTimeout(() => setCopied(false), 2000);
		} else {
			setError(t("copy_failed"));
		}
	};

	const expiresLabel = live?.expires_at
		? t("expires_on", {
				date: new Date(live.expires_at).toLocaleDateString(locale, {
					year: "numeric",
					month: "long",
					day: "numeric",
				}),
			})
		: t("never_expires");

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent
				className="w-[92vw] max-w-[92vw] sm:max-w-md"
				data-testid="notebook-share-dialog"
			>
				<DialogHeader>
					<DialogTitle className="flex items-center gap-2">
						<Globe className="size-4" />
						{tn("title")}
					</DialogTitle>
					<DialogDescription className="break-words">
						{tn("description", { title: notebookTitle })}
					</DialogDescription>
				</DialogHeader>

				<div className="space-y-4">
					<div
						role="note"
						className="flex gap-3 rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-700/60 dark:bg-amber-950/40 dark:text-amber-100"
					>
						<AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden />
						<div className="space-y-1">
							<p className="font-medium">{tn("warning_title")}</p>
							<p>{tn("warning_body")}</p>
						</div>
					</div>

					{live ? (
						<div className="space-y-2">
							<div className="flex items-center gap-2">
								<Input
									readOnly
									value={url}
									aria-label={t("link_label")}
									onFocus={(event) => event.currentTarget.select()}
									className="font-mono text-xs"
									data-testid="notebook-share-url"
								/>
								<Button type="button" variant="outline" onClick={() => void copy()}>
									{copied ? <Check className="size-4" /> : <Copy className="size-4" />}
									{copied ? t("copied") : t("copy")}
								</Button>
							</div>
							<p className="text-sm text-muted-foreground">
								{t("read_only")} · {expiresLabel}
							</p>
						</div>
					) : null}

					<div className="space-y-2">
						<Label htmlFor={expiryId}>{t("expiry_label")}</Label>
						<Select
							value={expiry}
							onValueChange={(value) => {
								if (isExpiryChoice(value)) setExpiry(value);
							}}
						>
							<SelectTrigger id={expiryId} className="w-full">
								<SelectValue />
							</SelectTrigger>
							<SelectContent>
								{EXPIRY_CHOICES.map((choice) => (
									<SelectItem key={choice} value={choice}>
										{t(`expiry.${choice}`)}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
						<p className="text-xs text-muted-foreground">{tn("live_note")}</p>
					</div>

					{live ? (
						<div className="flex flex-wrap items-center justify-between gap-2 border-t pt-3">
							<Button
								type="button"
								variant="ghost"
								size="sm"
								onClick={() => void save(true)}
								disabled={busy !== null}
								title={tn("regenerate_hint")}
							>
								{busy === "regenerate" && showSpinner ? (
									<Spinner size="sm" />
								) : (
									<RefreshCw className="size-4" />
								)}
								{tn("regenerate")}
							</Button>
							{live.can_revoke ? (
								<StopSharingButton
									workspaceId={workspaceId}
									notebookId={notebookId}
									onStopped={() => {
										onChange(null);
										onOpenChange(false);
									}}
								/>
							) : null}
						</div>
					) : null}
				</div>

				{notice ? <output className="block text-sm text-muted-foreground">{notice}</output> : null}
				{error ? (
					<p role="alert" className="text-sm text-destructive">
						{error}
					</p>
				) : null}

				<DialogFooter className="gap-2 sm:justify-between">
					<Link
						href={`/dashboard/${workspaceId}/workspace-settings/public-links`}
						className="self-center text-sm text-primary underline-offset-4 hover:underline"
						onClick={() => onOpenChange(false)}
					>
						{t("manage_links")}
					</Link>
					<div className="flex gap-2">
						<Button type="button" variant="secondary" onClick={() => onOpenChange(false)}>
							{live ? t("done") : t("cancel")}
						</Button>
						<Button
							type="button"
							onClick={() => void save(false)}
							disabled={busy !== null}
							data-testid="notebook-share-submit"
						>
							{busy === "save" && showSpinner ? <Spinner size="sm" /> : null}
							{live ? tn("save") : tn("create")}
						</Button>
					</div>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
