"use client";

import { useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Check, Copy, Globe } from "lucide-react";
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
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { publicLinkUrl } from "../lib/public-link-url";
import type { CreatedLink } from "../model/created-link";
import {
	DEFAULT_EXPIRY,
	EXPIRY_CHOICES,
	type ExpiryChoice,
	isExpiryChoice,
} from "../model/share-expiry";

function shareFailureMessage(
	failure: unknown,
	subject: "chat" | "output",
	t: (key: string) => string
): string {
	const status = (failure as { status?: number } | null)?.status;
	if (status === 400) return subject === "chat" ? t("empty_chat") : t("output_not_ready");
	if (status === 403) return t("not_allowed");
	return t("create_failed");
}

/**
 * Create a read-only public link to a chat or a Studio output.
 *
 * The warning sits above the button, not in a popup: anyone with the link can
 * read what is shared without signing in.
 */
export function ShareDialog({
	open,
	onOpenChange,
	workspaceId,
	subject,
	itemTitle,
	create,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	workspaceId: number;
	subject: "chat" | "output";
	itemTitle: string;
	create: (expiry: ExpiryChoice) => Promise<CreatedLink>;
}) {
	const t = useTranslations("publicSharing");
	const locale = useLocale();
	const queryClient = useQueryClient();
	const expiryId = useId();
	const [expiry, setExpiry] = useState<ExpiryChoice>(DEFAULT_EXPIRY);
	const [creating, setCreating] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [created, setCreated] = useState<CreatedLink | null>(null);
	const [copied, setCopied] = useState(false);
	const showSpinner = useDelayedFlag(creating);

	// Each opening starts fresh.
	useEffect(() => {
		if (!open) return;
		setExpiry(DEFAULT_EXPIRY);
		setCreating(false);
		setError(null);
		setCreated(null);
		setCopied(false);
	}, [open]);

	const url = created
		? publicLinkUrl(
				typeof window === "undefined" ? "" : window.location.origin,
				created.share_token
			)
		: "";

	const submit = async () => {
		if (creating) return;
		setCreating(true);
		setError(null);
		try {
			const link = await create(expiry);
			setCreated(link);
			void queryClient.invalidateQueries({ queryKey: cacheKeys.publicChatSnapshots.all });
		} catch (failure) {
			setError(shareFailureMessage(failure, subject, t));
		} finally {
			setCreating(false);
		}
	};

	const copy = async () => {
		try {
			await navigator.clipboard.writeText(url);
			setCopied(true);
			window.setTimeout(() => setCopied(false), 2000);
		} catch {
			setError(t("copy_failed"));
		}
	};

	const expiresLabel = created?.expires_at
		? t("expires_on", {
				date: new Date(created.expires_at).toLocaleDateString(locale, {
					year: "numeric",
					month: "long",
					day: "numeric",
				}),
			})
		: t("never_expires");

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="w-[92vw] max-w-[92vw] sm:max-w-md" data-testid="share-dialog">
				<DialogHeader>
					<DialogTitle className="flex items-center gap-2">
						<Globe className="size-4" />
						{subject === "chat" ? t("title_chat") : t("title_output")}
					</DialogTitle>
					<DialogDescription className="break-words">
						{t("description", { title: itemTitle })}
					</DialogDescription>
				</DialogHeader>

				{created ? (
					<div className="space-y-3">
						<div className="flex items-center gap-2">
							<Input
								readOnly
								value={url}
								aria-label={t("link_label")}
								onFocus={(event) => event.currentTarget.select()}
								className="font-mono text-xs"
							/>
							<Button type="button" variant="outline" onClick={() => void copy()}>
								{copied ? <Check className="size-4" /> : <Copy className="size-4" />}
								{copied ? t("copied") : t("copy")}
							</Button>
						</div>
						<p className="text-sm text-muted-foreground">
							{t("read_only")} · {expiresLabel}
						</p>
						<p className="text-sm">
							<Link
								href={`/dashboard/${workspaceId}/workspace-settings/public-links`}
								className="text-primary underline-offset-4 hover:underline"
								onClick={() => onOpenChange(false)}
							>
								{t("manage_links")}
							</Link>
						</p>
					</div>
				) : (
					<div className="space-y-4">
						<div
							role="note"
							className="flex gap-3 rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-700/60 dark:bg-amber-950/40 dark:text-amber-100"
						>
							<AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden />
							<div className="space-y-1">
								<p className="font-medium">{t("warning_title")}</p>
								<p>{t("warning_body")}</p>
							</div>
						</div>
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
							<p className="text-xs text-muted-foreground">{t("snapshot_note")}</p>
						</div>
					</div>
				)}
				{error ? (
					<p role="alert" className="text-sm text-destructive">
						{error}
					</p>
				) : null}

				<DialogFooter>
					{created ? (
						<Button type="button" onClick={() => onOpenChange(false)}>
							{t("done")}
						</Button>
					) : (
						<>
							<Button type="button" variant="secondary" onClick={() => onOpenChange(false)}>
								{t("cancel")}
							</Button>
							<Button type="button" onClick={() => void submit()} disabled={creating}>
								{showSpinner ? <Spinner size="sm" /> : null}
								{t("create")}
							</Button>
						</>
					)}
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
