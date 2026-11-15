"use client";

import type { ToolCallMessagePartProps } from "@assistant-ui/react";
import { MailIcon } from "lucide-react";
import { useFormatter, useTranslations } from "next-intl";
import type { FC, ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { cn } from "@/lib/utils";
import type { DraftStatus, MailDraft } from "../api/mail-draft-api";
import { type DraftNotice, type SettledOutcome, useMailDraft } from "../hooks/use-mail-draft";
import { useUnusualTicks } from "../hooks/use-unusual-ticks";
import { parseDraftCardPayload } from "../lib/draft-card-payload";
import { DraftBody } from "./draft-body";
import { UnusualConfirm } from "./unusual-confirm";

// The header line for each state, as keys in the `mail_draft` messages.
const TITLE_KEYS: Record<DraftStatus, string> = {
	pending: "title_pending",
	sending: "title_sending",
	sent: "title_sent",
	failed: "title_failed",
	discarded: "title_discarded",
	superseded: "title_superseded",
	expired: "title_expired",
};

// A draft that can no longer go anywhere is drawn quieter: it is a record.
const SETTLED: ReadonlySet<DraftStatus> = new Set(["discarded", "superseded", "expired"]);

function Frame({
	children,
	muted,
	className,
}: {
	children: ReactNode;
	muted?: boolean;
	className?: string;
}) {
	return (
		<div
			className={cn(
				"my-4 w-full max-w-lg overflow-hidden rounded-2xl border bg-muted/30",
				muted && "opacity-80",
				className
			)}
			data-testid="mail-draft-card"
		>
			{children}
		</div>
	);
}

const Divider = () => <div className="mx-4 h-px bg-border/50 sm:mx-5" />;

function Subtitle({ draft }: { draft: MailDraft }) {
	const t = useTranslations("mail_draft");
	const format = useFormatter();
	const when = (iso: string | null) =>
		iso ? format.dateTime(new Date(iso), { dateStyle: "medium", timeStyle: "short" }) : "";
	switch (draft.status) {
		case "pending":
			return <>{t("subtitle_pending")}</>;
		case "sending":
			return <TextWithSpinner text={t("subtitle_sending")} />;
		case "sent":
			return <>{t("subtitle_sent", { when: when(draft.sent_at) })}</>;
		case "failed":
			return <>{t("subtitle_failed")}</>;
		case "discarded":
			return <>{t("subtitle_discarded")}</>;
		case "superseded":
			return <>{t("subtitle_superseded")}</>;
		case "expired":
			return <>{t("subtitle_expired")}</>;
	}
}

function TextWithSpinner({ text }: { text: string }) {
	return (
		<span className="inline-flex items-center gap-1.5">
			<Spinner size="xs" />
			{text}
		</span>
	);
}

function Notice({ notice }: { notice: DraftNotice }) {
	const t = useTranslations("mail_draft");
	return (
		<div
			role={notice.tone === "error" ? "alert" : "status"}
			className={cn(
				"mx-4 mt-3 rounded-md px-3 py-2 text-xs sm:mx-5",
				notice.tone === "error"
					? "bg-destructive/10 text-destructive"
					: "bg-amber-500/10 text-amber-800 dark:text-amber-300"
			)}
		>
			<p>{t(notice.messageKey)}</p>
			{notice.detail && <p className="mt-1 break-words opacity-90">{notice.detail}</p>}
		</div>
	);
}

/**
 * The confirmation card for one drafted email. It shows the stored draft
 * the server returns (never the model's arguments) and is the only way the
 * message is sent: a click on Send, with the server's confirmation for this
 * exact content. Enter does nothing here on purpose, so a keystroke meant for
 * the chat box can never send mail.
 */
export const MailDraftCard: FC<{
	draftId: string;
	/** Told when Send or Discard ends the draft (the Drafts waiting page's list). */
	onSettled?: (outcome: SettledOutcome) => void;
	/** The frame's outer spacing: the chat's my-4 unless the host lays out its own. */
	className?: string;
}> = ({ draftId, onSettled, className }) => {
	const t = useTranslations("mail_draft");
	const { state, send, discard } = useMailDraft(draftId, onSettled);
	const confirmationNow = state.phase === "ready" ? state.view.confirmation : null;
	const unusual = useUnusualTicks(confirmationNow);

	if (state.phase === "loading") {
		return (
			<Frame className={className}>
				<div className="flex items-center gap-2 px-4 py-4 text-sm text-muted-foreground sm:px-5">
					<Spinner size="sm" />
					{t("loading")}
				</div>
			</Frame>
		);
	}
	if (state.phase === "unavailable") {
		return (
			<Frame muted className={className}>
				<div className="flex items-center gap-2 px-4 py-4 text-sm text-muted-foreground sm:px-5">
					<MailIcon className="size-4 shrink-0" aria-hidden />
					{t("unavailable")}
				</div>
			</Frame>
		);
	}

	const { view, busy, notice } = state;
	const { draft, confirmation } = view;
	const canAct = draft.status === "pending" || draft.status === "failed";
	const sending = busy === "send" || draft.status === "sending";

	return (
		<Frame muted={SETTLED.has(draft.status)} className={className}>
			<div className="flex items-start gap-3 px-4 pt-4 pb-3 select-none sm:px-5">
				<MailIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
				<div className="min-w-0">
					<p
						className={cn(
							"text-sm font-semibold",
							draft.status === "failed" ? "text-destructive" : "text-foreground"
						)}
					>
						{t(TITLE_KEYS[draft.status])}
					</p>
					<p className="mt-0.5 text-xs text-muted-foreground">
						{busy === "send" ? (
							<TextWithSpinner text={t("subtitle_sending")} />
						) : (
							<Subtitle draft={draft} />
						)}
					</p>
					{draft.status === "failed" && draft.failure && (
						<p className="mt-1 break-words text-xs text-destructive">{draft.failure}</p>
					)}
					{canAct && draft.blocked && (
						<p
							className="mt-1 break-words text-xs text-destructive"
							data-testid="mail-draft-blocked"
						>
							{draft.blocked}
						</p>
					)}
				</div>
			</div>
			{notice && <Notice notice={notice} />}
			<Divider />
			<DraftBody view={view} actionable={canAct} />

			{canAct && (
				<>
					<Divider />
					{confirmation && (
						<UnusualConfirm
							points={unusual.points}
							ticked={unusual.ticked}
							onToggle={unusual.toggle}
							disabled={busy !== null || sending}
						/>
					)}
					<div className="flex flex-wrap items-center gap-2 px-4 py-3 select-none sm:px-5">
						{confirmation ? (
							<Button
								type="button"
								size="sm"
								className="relative rounded-lg"
								onClick={() => void send([...unusual.ticked])}
								disabled={busy !== null || sending || !unusual.allTicked}
								data-testid="mail-draft-send"
							>
								<span className={busy === "send" ? "opacity-0" : ""}>
									{unusual.points.length > 0
										? t("send_anyway")
										: draft.status === "failed"
											? t("send_again")
											: t("send")}
								</span>
								{busy === "send" && <Spinner size="xs" className="absolute" />}
							</Button>
						) : (
							<p className="text-xs text-muted-foreground">{t("cannot_send_now")}</p>
						)}
						<Button
							type="button"
							variant="ghost"
							size="sm"
							className="relative rounded-lg text-muted-foreground"
							onClick={() => void discard()}
							disabled={busy !== null || sending}
							data-testid="mail-draft-discard"
						>
							<span className={busy === "discard" ? "opacity-0" : ""}>{t("discard")}</span>
							{busy === "discard" && <Spinner size="xs" className="absolute" />}
						</Button>
						{draft.status === "failed" && (
							<p className="w-full text-xs text-muted-foreground">{t("check_sent_folder")}</p>
						)}
					</div>
				</>
			)}
		</Frame>
	);
};

/**
 * draft_email's card in an answer. Only a finished call whose result is the
 * card payload draws one; anything else stays in the turn's trace.
 */
export function MailDraftToolUI({ result }: ToolCallMessagePartProps<unknown, unknown>) {
	const payload = parseDraftCardPayload(result);
	return payload ? <MailDraftCard draftId={payload.draft_id} /> : null;
}
