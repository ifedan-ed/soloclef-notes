"use client";

import { TriangleAlert } from "lucide-react";
import { useTranslations } from "next-intl";
import { useSourceFailure } from "../../hooks/use-source-failure";

/**
 * In place of the guide of a source that could not be read: that it failed,
 * and the server's reason, so the person knows whether to re-index it with
 * another parser or add a different file. "No guide yet" was all it said.
 */
export function SourceFailure({ documentId }: { documentId: number }) {
	const t = useTranslations("notebooks");
	const failure = useSourceFailure(documentId, true);
	return (
		<div className="space-y-1.5 text-sm" data-testid="source-failure">
			<p className="flex items-start gap-2 font-medium text-destructive">
				<TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden />
				{t("source_failed_title")}
			</p>
			{failure.data ? <p className="break-words text-muted-foreground">{failure.data}</p> : null}
		</div>
	);
}
