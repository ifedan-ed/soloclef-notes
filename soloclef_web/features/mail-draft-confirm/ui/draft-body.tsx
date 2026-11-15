"use client";

import { AlertTriangleIcon } from "lucide-react";
import { useTranslations } from "next-intl";
import { useMemo } from "react";
import type { MailDraftView } from "../api/mail-draft-api";
import { DraftAttachments } from "./draft-attachments";
import { DraftRecipients } from "./draft-recipients";

/**
 * The message a draft would send, field by field, from the server's stored
 * draft: sender, every recipient in full (with the ones outside the replied-to
 * conversation flagged), subject, the body as plain text and the attachments.
 * It draws text only; nothing in a draft is ever rendered as markup.
 */
export function DraftBody({ view, actionable }: { view: MailDraftView; actionable: boolean }) {
	const t = useTranslations("mail_draft");
	const { draft } = view;
	const outside = useMemo(
		() => new Set(draft.outside_thread.map((email) => email.toLowerCase())),
		[draft.outside_thread]
	);
	const from = draft.from.name ? `${draft.from.name} <${draft.from.email}>` : draft.from.email;
	return (
		<>
			<div className="space-y-2 px-4 py-3 sm:px-5">
				<div className="grid grid-cols-[3.5rem_minmax(0,1fr)] gap-x-3 text-sm">
					<p className="pt-0.5 text-xs text-muted-foreground">{t("from")}</p>
					<p className="min-w-0 [overflow-wrap:anywhere]">
						{from}
						{draft.account.label && draft.account.label !== draft.from.email && (
							<span className="text-muted-foreground"> · {draft.account.label}</span>
						)}
					</p>
				</div>
				<DraftRecipients label={t("to")} addresses={draft.to} outside={outside} />
				<DraftRecipients label={t("cc")} addresses={draft.cc} outside={outside} />
				<DraftRecipients
					label={t("bcc")}
					note={t("bcc_note")}
					addresses={draft.bcc}
					outside={outside}
				/>
				{/* The warning is advice before sending; on a settled draft it would mislead. */}
				{actionable && outside.size > 0 && (
					<div className="flex items-start gap-2 rounded-md bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-300">
						<AlertTriangleIcon className="mt-px size-3.5 shrink-0" aria-hidden />
						<p>{t("outside_thread_warning")}</p>
					</div>
				)}
			</div>
			<div className="mx-4 h-px bg-border/50 sm:mx-5" />
			<div className="space-y-2 px-4 py-3 sm:px-5">
				{draft.reply_to && (
					<p className="break-words text-xs text-muted-foreground">
						{t("reply_context", { subject: draft.reply_to.subject, from: draft.reply_to.from })}
					</p>
				)}
				<p className="break-words text-sm font-medium">
					{draft.subject || <span className="text-muted-foreground">{t("no_subject")}</span>}
				</p>
				{/* The body exactly as it will be sent: plain text, never markup. */}
				<div className="max-h-80 overflow-y-auto rounded-md border border-border/50 bg-background/60 px-3 py-2">
					<p className="whitespace-pre-wrap break-words text-sm leading-relaxed">
						{draft.body || <span className="text-muted-foreground">{t("no_body")}</span>}
					</p>
				</div>
				<DraftAttachments attachments={draft.attachments} />
			</div>
		</>
	);
}
