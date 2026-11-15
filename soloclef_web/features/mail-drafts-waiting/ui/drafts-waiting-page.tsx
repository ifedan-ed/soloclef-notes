"use client";

import { AlertCircle, MailCheck } from "lucide-react";
import { useFormatter, useTranslations } from "next-intl";
import { useCallback } from "react";
import { toast } from "sonner";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import type { SettledOutcome } from "@/features/mail-draft-confirm/hooks/use-mail-draft";
import { MailDraftCard } from "@/features/mail-draft-confirm/ui/mail-draft-card";
import type { WaitingDraft } from "../api/waiting-drafts-api";
import { useWaitingDrafts } from "../hooks/use-waiting-drafts";

/**
 * Drafts waiting: every email the assistant drafted that still waits for
 * its owner, each on the same card the chat shows (the same single-use
 * confirmation, unusual-send ticks and attachments), sent or discarded
 * here.
 *
 * It is a page of its own, not a section of the inbox, because the inbox is
 * a dropdown of one-line rows: a card with a message body, recipients and
 * tick boxes needs the width and the scroll of a page, and the inbox row a
 * waiting draft raises links here (soloclef_assistant/maildraftnotice). It
 * is the one place a draft made by an automation can be sent from, because
 * the run's own conversation is deleted when the run ends.
 */
export function DraftsWaitingPage() {
	const t = useTranslations("mail_drafts_waiting");
	const { data, isLoading, isError, refetch, isRefetching, settle } = useWaitingDrafts();
	const drafts = data?.drafts ?? [];

	return (
		<div className="h-full overflow-y-auto">
			<div className="mx-auto w-full max-w-2xl px-4 pt-6 pb-16 sm:px-6 sm:pt-8">
				<header className="mb-6 space-y-1.5">
					<div className="flex items-center gap-2">
						<h1 className="text-xl font-semibold tracking-tight">{t("title")}</h1>
						{drafts.length > 0 && (
							<span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium tabular-nums text-muted-foreground">
								{data?.more ? `${drafts.length}+` : drafts.length}
							</span>
						)}
					</div>
					<p className="max-w-prose text-sm leading-6 text-muted-foreground">{t("description")}</p>
				</header>

				{isLoading ? (
					<LoadingCards />
				) : isError ? (
					<Alert variant="destructive">
						<AlertCircle aria-hidden />
						<AlertDescription className="flex flex-wrap items-center gap-3">
							{t("load_failed")}
							<Button
								size="sm"
								variant="outline"
								className="h-7 rounded-full"
								onClick={() => void refetch()}
								disabled={isRefetching}
							>
								{t("retry")}
							</Button>
						</AlertDescription>
					</Alert>
				) : drafts.length === 0 ? (
					<NothingWaiting />
				) : (
					<ul className="space-y-8" data-testid="drafts-waiting-list">
						{drafts.map((draft) => (
							<WaitingDraftItem key={draft.id} draft={draft} onSettled={settle} />
						))}
					</ul>
				)}

				{data?.more && drafts.length > 0 && (
					<p className="mt-8 text-center text-xs text-muted-foreground">{t("more")}</p>
				)}
			</div>
		</div>
	);
}

/** One draft: the line that says where and when it was made, then its card. */
function WaitingDraftItem({
	draft,
	onSettled,
}: {
	draft: WaitingDraft;
	onSettled: (draftId: string) => void;
}) {
	const t = useTranslations("mail_drafts_waiting");
	const format = useFormatter();
	const when = (iso: string) =>
		format.dateTime(new Date(iso), { dateStyle: "medium", timeStyle: "short" });

	// The card has already said "sent" or "discarded" by the time this runs;
	// the toast carries that word past the card's leaving.
	const settled = useCallback(
		(outcome: SettledOutcome) => {
			toast.success(outcome === "sent" ? t("sent_toast") : t("discarded_toast"));
			onSettled(draft.id);
		},
		[draft.id, onSettled, t]
	);

	return (
		<li data-testid="drafts-waiting-item">
			<p className="flex flex-wrap gap-x-2 gap-y-0.5 px-1 text-xs text-muted-foreground">
				<span className="font-medium text-foreground/80">
					{t("from_mailbox", { mailbox: draft.account_label || t("unnamed_mailbox") })}
				</span>
				<span aria-hidden>·</span>
				<span>{t("drafted", { when: when(draft.created_at) })}</span>
				{draft.status !== "sending" && (
					<>
						<span aria-hidden>·</span>
						<span>{t("expires", { when: when(draft.expires_at) })}</span>
					</>
				)}
			</p>
			{/* The page lays the cards out itself, and gives each the full column. */}
			<MailDraftCard draftId={draft.id} onSettled={settled} className="mt-2 mb-0 max-w-none" />
		</li>
	);
}

function NothingWaiting() {
	const t = useTranslations("mail_drafts_waiting");
	return (
		<div
			className="flex flex-col items-center gap-3 rounded-2xl border border-dashed px-6 py-14 text-center"
			data-testid="drafts-waiting-empty"
		>
			<span className="flex size-12 items-center justify-center rounded-2xl bg-muted text-muted-foreground">
				<MailCheck className="size-6" aria-hidden />
			</span>
			<p className="text-base font-semibold">{t("empty_title")}</p>
			<p className="max-w-sm text-sm leading-6 text-muted-foreground">{t("empty_body")}</p>
		</div>
	);
}

function LoadingCards() {
	return (
		<div className="space-y-8" aria-busy>
			{["first", "second"].map((key) => (
				<div key={key} className="space-y-2">
					<Skeleton className="h-3 w-48" />
					<Skeleton className="h-56 w-full rounded-2xl" />
				</div>
			))}
		</div>
	);
}
