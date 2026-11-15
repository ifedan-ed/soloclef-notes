"use client";

import { ShieldAlertIcon } from "lucide-react";
import { useTranslations } from "next-intl";
import type { FC } from "react";
import { Checkbox } from "@/components/ui/checkbox";
import type { UnusualPoint } from "../api/mail-draft-api";
import { fileSize } from "../lib/file-size";

interface UnusualConfirmProps {
	points: UnusualPoint[];
	ticked: ReadonlySet<string>;
	onToggle: (code: string, on: boolean) => void;
	disabled: boolean;
}

/**
 * The second confirmation an unusual send needs: each unusual point in
 * plain words, each with its own box to tick. Send stays off until every
 * box is ticked, and the server refuses a send that does not name them all,
 * so this is a step the person takes, not a notice they scroll past.
 */
export const UnusualConfirm: FC<UnusualConfirmProps> = ({ points, ticked, onToggle, disabled }) => {
	const t = useTranslations("mail_draft");
	if (points.length === 0) return null;
	return (
		<div
			className="mx-4 mb-1 mt-3 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-3 sm:mx-5"
			data-testid="mail-draft-unusual"
		>
			<div className="flex items-start gap-2">
				<ShieldAlertIcon
					className="mt-0.5 size-4 shrink-0 text-amber-700 dark:text-amber-400"
					aria-hidden
				/>
				<div className="min-w-0">
					<p className="text-sm font-medium text-amber-900 dark:text-amber-200">
						{t("unusual_title")}
					</p>
					<p className="mt-0.5 text-xs text-amber-800 dark:text-amber-300">{t("unusual_hint")}</p>
				</div>
			</div>
			<ul className="mt-3 space-y-2.5">
				{points.map((point) => {
					const id = `unusual-${point.code}`;
					return (
						<li key={point.code} className="flex items-start gap-2.5">
							<Checkbox
								id={id}
								className="mt-0.5 bg-background"
								checked={ticked.has(point.code)}
								onCheckedChange={(state) => onToggle(point.code, state === true)}
								disabled={disabled}
								data-testid={`mail-draft-unusual-${point.code}`}
							/>
							<label
								htmlFor={id}
								className="min-w-0 cursor-pointer text-xs leading-relaxed text-foreground"
							>
								<UnusualSentence point={point} />
							</label>
						</li>
					);
				})}
			</ul>
		</div>
	);
};

/** One point in the card's own words, or the server's for a code it does not know. */
function UnusualSentence({ point }: { point: UnusualPoint }) {
	const t = useTranslations("mail_draft");
	const addresses = (point.addresses ?? []).join(", ");
	switch (point.code) {
		case "many_recipients":
			return (
				<>{t("unusual_many_recipients", { count: point.count ?? 0, limit: point.limit ?? 0 })}</>
			);
		case "many_sends":
			return <>{t("unusual_many_sends", { count: point.count ?? 0, limit: point.limit ?? 0 })}</>;
		case "new_recipients":
			return (
				<>
					{t("unusual_new_recipients", { count: point.addresses?.length ?? 0 })}{" "}
					<span className="font-medium [overflow-wrap:anywhere]">{addresses}</span>
				</>
			);
		case "recipients_unchecked":
			return (
				<>
					{t("unusual_recipients_unchecked")}{" "}
					<span className="font-medium [overflow-wrap:anywhere]">{addresses}</span>
				</>
			);
		case "large_attachments": {
			const size = fileSize(point.bytes ?? 0);
			return <>{t("unusual_large_attachments", { size: t(size.key, { size: size.value }) })}</>;
		}
		default:
			return <>{point.message}</>;
	}
}
