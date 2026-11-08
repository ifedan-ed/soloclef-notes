"use client";

import { useThreadRuntime } from "@assistant-ui/react";
import {
	GraduationCap,
	Library,
	Lightbulb,
	MessageCircleQuestion,
	Rocket,
	Search,
} from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import { useStudioSuggestions } from "../../hooks/use-notebook-queries";
import { GUIDED_STARTS, type GuidedStart } from "../../lib/guided-starts";
import { formatNotebookDate } from "../../lib/notebook-dates";
import { useNotebookChat } from "../../state/notebook-chat-context";

const START_ICONS: Record<GuidedStart, typeof Lightbulb> = {
	learn: GraduationCap,
	create: Lightbulb,
	progress: Rocket,
};

/**
 * An empty notebook chat: the notebook's title and source count, then ways
 * in. With sources, three questions drawn from them; without, three guided
 * starts. Replaces the regular chat greeting inside notebooks; the composer
 * stays underneath.
 */
export function NotebookWelcome({ composer }: { composer: ReactNode }) {
	const notebook = useNotebookChat();
	const t = useTranslations("notebooks");
	const locale = useLocale();
	const thread = useThreadRuntime();
	const hasSources = (notebook?.enabledSourceCount ?? 0) > 0;
	const questions = useStudioSuggestions(
		notebook?.workspaceId ?? 0,
		notebook?.notebookId ?? 0,
		"chat",
		{},
		!!notebook && hasSources
	);
	const showSkeleton = useDelayedFlag(hasSources && questions.isLoading, 400);
	if (!notebook) return null;

	const ask = (text: string) => {
		thread.composer.setText(text);
		thread.composer.send();
	};
	const suggested = questions.data?.suggestions ?? [];
	// Guided starts suit an empty notebook, or sources whose questions failed.
	const guided = !hasSources || (!questions.isLoading && suggested.length === 0);
	const meta = [
		t("source_count", { count: notebook.sourceCount }),
		formatNotebookDate(notebook.createdAt, locale),
	]
		.filter(Boolean)
		.join(" · ");

	return (
		<div className="flex min-h-0 w-full flex-1 overflow-y-auto">
			<section className="mx-auto flex w-full max-w-(--thread-max-width) flex-col gap-6 px-4 pt-10 pb-8">
				<div className="space-y-2">
					<span className="text-5xl leading-none" aria-hidden>
						{notebook.icon || "📓"}
					</span>
					<h1 className="text-3xl font-semibold text-balance break-words md:text-4xl">
						{notebook.title}
					</h1>
					<p className="text-sm text-muted-foreground">{meta}</p>
				</div>
				<div className="space-y-3 rounded-2xl border bg-muted/30 p-5">
					<p className="text-base font-medium">
						{hasSources ? t("welcome.heading_with_sources") : t("welcome.heading")}
					</p>
					<p className="text-sm text-muted-foreground">
						{hasSources ? t("welcome.body_with_sources") : t("welcome.body_no_sources")}
					</p>
					{guided ? (
						<div className="flex flex-wrap gap-2">
							{GUIDED_STARTS.map((kind) => {
								const Icon = START_ICONS[kind];
								return (
									<button
										key={kind}
										type="button"
										onClick={() => ask(t(`welcome.starts.${kind}.prompt`))}
										className="inline-flex items-center gap-2 rounded-full border bg-background px-3.5 py-2 text-sm transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
									>
										<Icon className="size-4 text-muted-foreground" aria-hidden />
										{t(`welcome.starts.${kind}.label`)}
									</button>
								);
							})}
						</div>
					) : suggested.length > 0 ? (
						<nav aria-label={t("welcome.questions_label")} className="grid gap-2">
							{suggested.map((question) => (
								<button
									key={question}
									type="button"
									onClick={() => ask(question)}
									className="flex items-start gap-2 rounded-xl border bg-background p-3 text-left text-sm transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
								>
									<MessageCircleQuestion
										className="mt-0.5 size-4 shrink-0 text-muted-foreground"
										aria-hidden
									/>
									<span>{question}</span>
								</button>
							))}
						</nav>
					) : showSkeleton ? (
						<div className="grid gap-2" aria-busy="true">
							{["a", "b", "c"].map((key) => (
								<Skeleton key={key} className="h-11 rounded-xl" />
							))}
						</div>
					) : null}
					{notebook.enabledSourceCount === 0 ? (
						<div className="flex flex-wrap gap-2 pt-1">
							{notebook.addSources ? (
								<Button variant="ghost" size="sm" onClick={notebook.addSources}>
									<Library className="size-4" />
									{t("add_sources")}
								</Button>
							) : null}
							{notebook.openResearch ? (
								<Button variant="ghost" size="sm" onClick={notebook.openResearch}>
									<Search className="size-4" />
									{t("research_the_web")}
								</Button>
							) : null}
						</div>
					) : null}
				</div>
				<div className="w-full">{composer}</div>
			</section>
		</div>
	);
}
