"use client";

import { useTranslations } from "next-intl";
import type { FC } from "react";
import type { DraftAddress } from "../api/mail-draft-api";

interface DraftRecipientsProps {
	label: string;
	/** Said under the label: Bcc's "hidden from the other recipients". */
	note?: string;
	addresses: DraftAddress[];
	/** Lower-cased addresses that were not on the message this replies to. */
	outside: ReadonlySet<string>;
}

/**
 * One recipient field, every address in full. Nothing is folded behind a
 * "+3": a recipient the person did not mean is exactly the one a summary
 * would hide, and an address the model was talked into adding by a message
 * it read is flagged where the person's eye already is.
 */
export const DraftRecipients: FC<DraftRecipientsProps> = ({ label, note, addresses, outside }) => {
	const t = useTranslations("mail_draft");
	if (addresses.length === 0) return null;
	return (
		<div className="grid grid-cols-[3.5rem_minmax(0,1fr)] gap-x-3 gap-y-1 text-sm">
			<div className="pt-0.5 text-xs text-muted-foreground">
				<p>{label}</p>
			</div>
			<div className="min-w-0 space-y-1">
				{note && <p className="text-xs text-muted-foreground">{note}</p>}
				<ul className="space-y-1">
					{addresses.map((address, index) => {
						const flagged = outside.has(address.email.toLowerCase());
						return (
							// The same address may be named twice; the list is fixed once drawn.
							// biome-ignore lint/suspicious/noArrayIndexKey: the server's order, never reordered
							<li key={`${index}:${address.email}`} className="min-w-0">
								<span className="[overflow-wrap:anywhere]">
									{address.name ? (
										<>
											<span className="text-foreground">{address.name}</span>{" "}
											<span className="text-muted-foreground">&lt;{address.email}&gt;</span>
										</>
									) : (
										<span className="text-foreground">{address.email}</span>
									)}
								</span>
								{flagged && (
									<span className="mt-0.5 block w-fit rounded bg-amber-500/15 px-1.5 py-px text-[11px] text-amber-700 dark:text-amber-400">
										{t("outside_thread_flag")}
									</span>
								)}
							</li>
						);
					})}
				</ul>
			</div>
		</div>
	);
};
