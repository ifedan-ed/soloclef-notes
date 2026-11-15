"use client";

import { PaperclipIcon } from "lucide-react";
import { useTranslations } from "next-intl";
import type { MailDraft } from "../api/mail-draft-api";
import { fileSize } from "../lib/file-size";

/**
 * Every file the message would carry, as the server stored it: the name it
 * goes out under, its size, and where it came from (a document, a Studio
 * output, a message it forwards). Nothing is folded away: a file the person
 * did not mean to send is exactly the one a summary would hide.
 */
export function DraftAttachments({ attachments }: { attachments: MailDraft["attachments"] }) {
	const t = useTranslations("mail_draft");
	if (attachments.length === 0) {
		return <p className="text-xs text-muted-foreground">{t("no_attachments")}</p>;
	}
	return (
		<div className="space-y-1.5">
			<p className="text-xs text-muted-foreground">
				{t("attachments", { count: attachments.length })}
			</p>
			<ul className="space-y-1.5" data-testid="mail-draft-attachments">
				{attachments.map((file, index) => {
					const size = fileSize(file.size);
					return (
						<li
							// biome-ignore lint/suspicious/noArrayIndexKey: the server's order, never reordered
							key={`${index}:${file.name}`}
							className="flex min-w-0 items-start gap-2 rounded-md border border-border/50 bg-background/60 px-2.5 py-1.5"
						>
							<PaperclipIcon
								className="mt-0.5 size-3.5 shrink-0 text-muted-foreground"
								aria-hidden
							/>
							<div className="min-w-0 flex-1">
								<p className="text-sm [overflow-wrap:anywhere]">
									{file.name}{" "}
									<span className="whitespace-nowrap text-xs text-muted-foreground">
										{t(size.key, { size: size.value })}
									</span>
								</p>
								{file.origin && (
									<p className="text-xs text-muted-foreground [overflow-wrap:anywhere]">
										{file.origin}
									</p>
								)}
							</div>
						</li>
					);
				})}
			</ul>
		</div>
	);
}
