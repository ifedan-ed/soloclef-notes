"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, FileText, Search, Sparkles } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { SegmentedControl } from "@/components/ui/segmented-control";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
import { billingSettingsPath, isBillingRefusal } from "@/features/billing/lib/billing-errors";
import { showBillingRefusalToast } from "@/features/billing/ui/billing-refusal-toast";
import { StudioCostHint } from "@/features/billing/ui/studio-cost-hint";
import { podcastsApiService } from "@/lib/apis/podcasts-api.service";
import { notebookKeys } from "../../api/notebook-query-keys";
import { notebooksApi } from "../../api/notebooks-api";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import { useReportTemplates, useStudioSuggestions } from "../../hooks/use-notebook-queries";
import { useSuggestionRotation } from "../../hooks/use-suggestion-rotation";
import { CUSTOM_STYLE_MAX_CHARS, studioTile } from "../../lib/studio-catalog";
import {
	appendSuggestion,
	buildStudioRequest,
	initialDialogState,
	needsStyleDescription,
	newRequestId,
	STUDIO_LANGUAGES,
	type StudioDialogState,
	type StudioRequestBody,
} from "../../lib/studio-request";
import { suggestionVariant, suggestionWindow } from "../../lib/suggestion-rotation";
import type { NotebookSource, ReportTemplate, StudioKind } from "../../model/notebook";
import { FocusCards } from "./focus-cards";
import { MoreIdeasButton } from "./more-ideas-button";
import { OptionCards } from "./option-cards";
import { ReportTemplates } from "./report-templates";
import { SourcePickerView } from "./source-picker-view";
import { StyleGallery } from "./style-gallery";

const DEFAULT_LANGUAGE = "__default__";

interface GenerateDialogProps {
	kind: StudioKind | null;
	onOpenChange: (open: boolean) => void;
	workspaceId: number;
	notebookId: number;
	notebookTitle: string;
	sources: NotebookSource[];
	onGenerate: (body: StudioRequestBody) => Promise<unknown>;
	onAddSources: () => void;
	onResearch: () => void;
}

export function GenerateDialog(props: GenerateDialogProps) {
	return (
		<Dialog open={props.kind !== null} onOpenChange={props.onOpenChange}>
			<DialogContent className="flex max-h-[90vh] w-[calc(100vw-2rem)] max-w-xl flex-col gap-4 overflow-y-auto select-text">
				{props.kind === null ? null : props.sources.length === 0 ? (
					<NeedsSources
						kind={props.kind}
						onAddSources={() => {
							props.onOpenChange(false);
							props.onAddSources();
						}}
						onResearch={() => {
							props.onOpenChange(false);
							props.onResearch();
						}}
					/>
				) : (
					<GenerateForm {...props} kind={props.kind} />
				)}
			</DialogContent>
		</Dialog>
	);
}

/** A tile opened before the notebook has any sources: say why, and offer the ways to add some. */
function NeedsSources(props: NeedsSourcesProps) {
	const t = useTranslations("notebooks");
	const Icon = studioTile(props.kind).icon;
	return (
		<>
			<DialogHeader>
				<DialogTitle className="flex items-center gap-2">
					<Icon className="size-5 text-muted-foreground" aria-hidden />
					{t(`kinds.${props.kind}.dialog_title`)}
				</DialogTitle>
				<DialogDescription className="sr-only">{t("needs_sources.title")}</DialogDescription>
			</DialogHeader>
			<NeedsSourcesBody {...props} />
		</>
	);
}

interface NeedsSourcesProps {
	kind: StudioKind;
	onAddSources: () => void;
	onResearch: () => void;
}

export function NeedsSourcesBody({ kind, onAddSources, onResearch }: NeedsSourcesProps) {
	const t = useTranslations("notebooks");
	return (
		<div className="space-y-4">
			<p className="text-sm text-muted-foreground">
				{t("needs_sources.body", { output: t(`kinds.${kind}.label`) })}
			</p>
			<div className="grid gap-2 sm:grid-cols-2">
				<Button
					variant="outline"
					className="h-auto justify-start gap-3 py-3"
					onClick={onAddSources}
				>
					<FileText className="size-4 shrink-0" aria-hidden />
					<span className="flex flex-col items-start text-left">
						<span className="font-medium">{t("add_sources")}</span>
						<span className="text-xs font-normal text-muted-foreground">
							{t("needs_sources.add_hint")}
						</span>
					</span>
				</Button>
				<Button variant="outline" className="h-auto justify-start gap-3 py-3" onClick={onResearch}>
					<Search className="size-4 shrink-0" aria-hidden />
					<span className="flex flex-col items-start text-left">
						<span className="font-medium">{t("research_the_web")}</span>
						<span className="text-xs font-normal text-muted-foreground">
							{t("needs_sources.research_hint")}
						</span>
					</span>
				</Button>
			</div>
		</div>
	);
}

function GenerateForm({
	kind,
	onOpenChange,
	workspaceId,
	notebookId,
	notebookTitle,
	sources,
	onGenerate,
}: GenerateDialogProps & { kind: StudioKind }) {
	const t = useTranslations("notebooks");
	const locale = useLocale();
	const queryClient = useQueryClient();
	const tile = studioTile(kind);
	const enabledIds = useMemo(
		() => sources.filter((source) => source.enabled).map((source) => source.id),
		[sources]
	);
	const [state, setState] = useState<StudioDialogState>(() => initialDialogState(kind, enabledIds));
	const [view, setView] = useState<"options" | "sources">("options");
	const [submitting, setSubmitting] = useState(false);
	const [requestId] = useState(newRequestId);
	const showSubmitting = useDelayedFlag(submitting);

	const variant = suggestionVariant(kind, state.options);
	const suggestions = useStudioSuggestions(
		workspaceId,
		notebookId,
		kind,
		{ variant, language: state.language },
		state.sourceIds.length > 0
	);
	const showSuggestionSkeleton = useDelayedFlag(suggestions.isLoading);
	const rotation = useSuggestionRotation(`${notebookId}:${kind}:${variant}:${state.language}`);
	const pool = suggestions.data?.pool.length
		? suggestions.data.pool
		: (suggestions.data?.suggestions ?? []);
	const examples = suggestions.data?.examples.length
		? suggestions.data.examples
		: suggestions.data?.placeholder
			? [suggestions.data.placeholder]
			: [];
	const { suggestions: suggestionList, placeholder: examplePlaceholder } = suggestionWindow(
		pool,
		examples,
		rotation.offset
	);
	const [refreshingIdeas, setRefreshingIdeas] = useState(false);
	const refreshingIdeasRef = useRef(false);
	// Steps through the cached pool; once all of it was shown, asks for a fresh
	// one. The current chips stay painted until it arrives, and a click while
	// one is on its way is ignored.
	const showMoreIdeas = async () => {
		if (refreshingIdeasRef.current || !rotation.advance(pool.length)) return;
		refreshingIdeasRef.current = true;
		setRefreshingIdeas(true);
		try {
			const fresh = await notebooksApi.suggestions(workspaceId, notebookId, kind, {
				variant,
				language: state.language,
				refresh: true,
			});
			queryClient.setQueryData(
				notebookKeys.suggestions(workspaceId, notebookId, kind, variant, state.language),
				fresh
			);
			rotation.restart();
		} catch {
			toast.error(t("more_ideas_failed"));
		} finally {
			refreshingIdeasRef.current = false;
			setRefreshingIdeas(false);
		}
	};
	const focusRef = useRef<HTMLTextAreaElement>(null);
	const templates = useReportTemplates(workspaceId, kind === "report");
	const podcastLanguages = useQuery({
		queryKey: ["podcast-languages"],
		queryFn: () => podcastsApiService.listLanguages(),
		enabled: kind === "audio",
		staleTime: 10 * 60_000,
	});

	const reportFormat = state.options.format;
	const visibleTemplates = (templates.data ?? []).filter(
		(template) => template.format === reportFormat
	);
	// Preselect the first template of the chosen report format.
	useEffect(() => {
		if (kind !== "report" || !templates.data) return;
		const current = templates.data.find((template) => template.key === state.templateKey);
		if (current && current.format === reportFormat) return;
		const first = templates.data.find((template) => template.format === reportFormat) ?? null;
		setState((s) => ({
			...s,
			templateKey: first?.key ?? null,
			templatePrompt: first?.prompt ?? null,
		}));
	}, [kind, templates.data, reportFormat, state.templateKey]);

	const languages = kind === "audio" ? (podcastLanguages.data?.languages ?? []) : STUDIO_LANGUAGES;
	const languageName = (tag: string) => {
		try {
			return new Intl.DisplayNames([locale], { type: "language" }).of(tag) ?? tag;
		} catch {
			return tag;
		}
	};

	const setOption = (field: string, value: string) =>
		setState((s) => ({ ...s, options: { ...s.options, [field]: value } }));

	const submit = async () => {
		if (state.sourceIds.length === 0) {
			toast.error(t("choose_sources"));
			return;
		}
		setSubmitting(true);
		try {
			await onGenerate(
				buildStudioRequest(state, {
					enabledSourceIds: enabledIds,
					title: `${t(`kinds.${kind}.label`)}: ${notebookTitle}`,
					requestId,
				})
			);
			onOpenChange(false);
		} catch (error) {
			if (isBillingRefusal(error)) {
				showBillingRefusalToast({
					code: (error as { code?: string }).code,
					message: error instanceof Error ? error.message : "",
					href: billingSettingsPath(workspaceId),
				});
			} else {
				toast.error(error instanceof Error && error.message ? error.message : t("generate_failed"));
			}
			setSubmitting(false);
		}
	};

	if (view === "sources") {
		return (
			<SourcePickerView
				sources={sources}
				selected={state.sourceIds}
				onBack={() => setView("options")}
				onConfirm={(ids) => {
					setState((s) => ({ ...s, sourceIds: ids }));
					setView("options");
				}}
			/>
		);
	}

	const Icon = tile.icon;
	return (
		<>
			<DialogHeader>
				<DialogTitle className="flex items-center gap-2">
					<Icon className="size-5 text-muted-foreground" aria-hidden />
					{t(`kinds.${kind}.dialog_title`)}
				</DialogTitle>
				<DialogDescription>{t(`kinds.${kind}.description`)}</DialogDescription>
			</DialogHeader>

			{tile.groups.map((group) => {
				if (kind === "report" && group.field === "file_type" && reportFormat !== "document") {
					return null;
				}
				const label = t(`fields.${group.field}`);
				const valueLabel = (value: string) => {
					const text = t(`options.${kind}.${group.field}.${value}.label`);
					return value === group.defaultValue ? `${text} ${t("default_suffix")}` : text;
				};
				return (
					<div key={group.field} className="space-y-2">
						{/* A pill has no room for "(Default)" on a phone, so segmented
						    groups say which option is the default beside the field label. */}
						<div className="flex items-baseline justify-between gap-3">
							<p className="text-sm font-medium">{label}</p>
							{group.presentation !== "gallery" &&
							group.presentation !== "cards" &&
							group.defaultValue ? (
								<p className="text-xs text-muted-foreground">
									{t("default_is", {
										value: t(`options.${kind}.${group.field}.${group.defaultValue}.label`),
									})}
								</p>
							) : null}
						</div>
						{group.presentation === "gallery" ? (
							<>
								<StyleGallery
									label={label}
									value={state.options[group.field]}
									onChange={(value) => setOption(group.field, value)}
									scrollLabels={{ previous: t("scroll_previous"), next: t("scroll_next") }}
									previewShape={group.previewShape}
									options={group.values.map((value) => ({
										value,
										label: valueLabel(value),
										description: t(`options.${kind}.${group.field}.${value}.description`),
										preview: group.previews?.[value],
									}))}
								/>
								{kind === "video" && state.options[group.field] === "custom" ? (
									<Textarea
										aria-label={t("custom_style_label")}
										value={state.stylePrompt ?? ""}
										onChange={(event) =>
											setState((s) => ({ ...s, stylePrompt: event.target.value }))
										}
										placeholder={t("custom_style_placeholder")}
										rows={2}
										maxLength={CUSTOM_STYLE_MAX_CHARS}
									/>
								) : null}
							</>
						) : group.presentation === "cards" ? (
							<OptionCards
								label={label}
								value={state.options[group.field]}
								onChange={(value) => setOption(group.field, value)}
								options={group.values.map((value) => ({
									value,
									label: valueLabel(value),
									description: t(`options.${kind}.${group.field}.${value}.description`),
									preview: group.previews?.[value],
								}))}
							/>
						) : (
							<SegmentedControl
								ariaLabel={label}
								value={state.options[group.field]}
								onValueChange={(value) => setOption(group.field, value)}
								// Pills keep to one line; the default is named on the label row.
								options={group.values.map((value) => ({
									value,
									ariaLabel: valueLabel(value),
									label: t(`options.${kind}.${group.field}.${value}.label`),
								}))}
							/>
						)}
					</div>
				);
			})}

			{kind === "report" ? (
				<ReportTemplates
					workspaceId={workspaceId}
					templates={visibleTemplates}
					selectedKey={state.templateKey ?? null}
					prompt={state.templatePrompt ?? null}
					onSelect={(template: ReportTemplate | null) =>
						setState((s) => ({
							...s,
							templateKey: template?.key ?? null,
							templatePrompt: template?.prompt ?? null,
						}))
					}
					onPromptChange={(prompt) => setState((s) => ({ ...s, templatePrompt: prompt }))}
					onSaved={(saved) =>
						queryClient.setQueryData(notebookKeys.reportTemplates(workspaceId), saved)
					}
				/>
			) : null}

			<div className="grid gap-3 sm:grid-cols-2">
				{tile.language ? (
					<div className="space-y-2">
						<p className="text-sm font-medium">{t("fields.language")}</p>
						<Select
							value={state.language || DEFAULT_LANGUAGE}
							onValueChange={(value) =>
								setState((s) => ({ ...s, language: value === DEFAULT_LANGUAGE ? "" : value }))
							}
						>
							<SelectTrigger aria-label={t("fields.language")}>
								<SelectValue />
							</SelectTrigger>
							<SelectContent>
								<SelectItem value={DEFAULT_LANGUAGE}>{t("language_default")}</SelectItem>
								{languages.map((tag) => (
									<SelectItem key={tag} value={tag}>
										{languageName(tag)}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					</div>
				) : null}
				<div className="space-y-2">
					<p className="text-sm font-medium">{t("sources")}</p>
					<Button
						variant="outline"
						className="w-full justify-between"
						onClick={() => setView("sources")}
					>
						{t("source_count", { count: state.sourceIds.length })}
						<ChevronRight className="size-4" />
					</Button>
				</div>
			</div>

			<div className="space-y-2">
				<label htmlFor="studio-focus" className="text-sm font-medium">
					{t(`kinds.${kind}.focus_label`)}
				</label>
				{!suggestions.isLoading && suggestionList.length > 0 ? (
					<div className="flex items-center justify-between gap-2">
						<p className="text-xs text-muted-foreground">{t(`kinds.${kind}.ideas_label`)}</p>
						<MoreIdeasButton
							label={t("more_ideas")}
							busy={refreshingIdeas}
							onClick={showMoreIdeas}
						/>
					</div>
				) : null}
				{kind === "video" && !suggestions.isLoading && suggestionList.length > 0 ? (
					<FocusCards
						label={t(`kinds.${kind}.focus_label`)}
						suggestions={suggestionList}
						focus={state.focus}
						onPick={(suggestion) => setState((s) => ({ ...s, focus: suggestion }))}
						onCustom={() => {
							setState((s) => ({
								...s,
								focus: suggestionList.includes(s.focus.trim()) ? "" : s.focus,
							}));
							focusRef.current?.focus();
						}}
						customLabel={t("custom_topic")}
						customDescription={t("custom_topic_description")}
					/>
				) : null}
				<Textarea
					id="studio-focus"
					ref={focusRef}
					value={state.focus}
					onChange={(event) => setState((s) => ({ ...s, focus: event.target.value }))}
					placeholder={
						examplePlaceholder
							? t("focus_example", { example: examplePlaceholder })
							: t(`kinds.${kind}.focus_placeholder`)
					}
					rows={3}
					maxLength={2000}
				/>
				{suggestions.isLoading ? (
					showSuggestionSkeleton ? (
						<div className="flex gap-2" aria-busy>
							<Skeleton className="h-7 w-40 rounded-full" />
							<Skeleton className="h-7 w-32 rounded-full" />
						</div>
					) : null
				) : kind !== "video" && suggestionList.length > 0 ? (
					<div className="flex flex-wrap gap-2">
						{suggestionList.map((suggestion) => (
							<button
								key={suggestion}
								type="button"
								onClick={() =>
									setState((s) => ({ ...s, focus: appendSuggestion(s.focus, suggestion) }))
								}
								className="inline-flex max-w-full items-center gap-1 rounded-full border px-3 py-1 text-left text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
							>
								<Sparkles className="size-3 shrink-0" aria-hidden />
								<span className="line-clamp-2">{suggestion}</span>
							</button>
						))}
					</div>
				) : null}
			</div>

			<StudioCostHint kind={kind} kindLabel={t(`kinds.${kind}.label`)} workspaceId={workspaceId} />

			<div className="flex justify-end gap-2 pt-1">
				<Button variant="ghost" onClick={() => onOpenChange(false)}>
					{t("cancel")}
				</Button>
				<Button
					onClick={submit}
					disabled={submitting || state.sourceIds.length === 0 || needsStyleDescription(state)}
				>
					{showSubmitting ? <Spinner size="sm" /> : null}
					{t("generate_now")}
				</Button>
			</div>
		</>
	);
}
