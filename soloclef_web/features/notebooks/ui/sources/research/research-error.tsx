"use client";

import { TriangleAlert } from "lucide-react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import type { ResearchSourceId } from "../../../model/research-sources";
import { useSourceFailureReason } from "./use-source-failure-reason";

/** A search that did not finish, worded for the user when the reason is a known one. */
export function ResearchError({
	message,
	code,
	source,
	onRetry,
	onDismiss,
}: {
	message: string | null;
	code?: string | null;
	source: ResearchSourceId;
	onRetry: () => void;
	onDismiss: () => void;
}) {
	const t = useTranslations("notebook_research");
	const reason = useSourceFailureReason()(source, code, message);
	return (
		<div role="alert" className="space-y-2 rounded-xl border border-destructive/30 px-3 py-2.5">
			<p className="flex items-start gap-2 text-xs text-destructive">
				<TriangleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden />
				<span className="min-w-0 break-words">
					{t("search_failed")}
					{reason ? <span className="block text-muted-foreground">{reason}</span> : null}
				</span>
			</p>
			<div className="flex justify-end gap-1">
				<Button type="button" variant="ghost" size="sm" className="h-7 text-xs" onClick={onDismiss}>
					{t("dismiss")}
				</Button>
				<Button type="button" variant="outline" size="sm" className="h-7 text-xs" onClick={onRetry}>
					{t("retry")}
				</Button>
			</div>
		</div>
	);
}
