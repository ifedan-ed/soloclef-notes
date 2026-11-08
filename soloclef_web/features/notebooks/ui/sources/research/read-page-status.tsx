"use client";

import { CheckCircle2, TriangleAlert } from "lucide-react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import type { PageReader } from "../../../model/research-sources";

export type ReadPageState =
	| { status: "idle" }
	| { status: "adding" }
	| { status: "added" }
	| { status: "failed"; message: string | null };

/** How "Read a web page" is going: adding, added (read in the background), or why not. */
export function ReadPageStatus({
	state,
	pageReader,
	onDismiss,
}: {
	state: ReadPageState;
	pageReader: PageReader;
	onDismiss: () => void;
}) {
	const t = useTranslations("notebook_research");
	if (state.status === "idle") return null;
	if (state.status === "adding") {
		return (
			<output className="flex items-center gap-2 px-1 text-xs text-muted-foreground">
				<Spinner size="xs" />
				{t("page_adding")}
			</output>
		);
	}
	if (state.status === "added") {
		return (
			<output className="flex items-start gap-2 rounded-xl border px-3 py-2.5 text-xs">
				<CheckCircle2 className="mt-0.5 size-3.5 shrink-0 text-emerald-600" aria-hidden />
				<span className="min-w-0 flex-1 break-words">
					{t("page_added")}
					<span className="block text-muted-foreground">
						{pageReader === "browserbase" ? t("page_hint_browserbase") : t("page_hint_crawler")}
					</span>
				</span>
				<Button type="button" variant="ghost" size="sm" className="h-7 text-xs" onClick={onDismiss}>
					{t("dismiss")}
				</Button>
			</output>
		);
	}
	return (
		<div
			role="alert"
			className="flex items-start gap-2 rounded-xl border border-destructive/30 px-3 py-2.5"
		>
			<TriangleAlert className="mt-0.5 size-3.5 shrink-0 text-destructive" aria-hidden />
			<span className="min-w-0 flex-1 break-words text-xs text-destructive">
				{t("page_failed")}
				{state.message ? (
					<span className="block text-muted-foreground">{state.message}</span>
				) : null}
			</span>
			<Button type="button" variant="ghost" size="sm" className="h-7 text-xs" onClick={onDismiss}>
				{t("dismiss")}
			</Button>
		</div>
	);
}
