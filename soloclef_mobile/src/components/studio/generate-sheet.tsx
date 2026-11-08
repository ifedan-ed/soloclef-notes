import { useQueryClient } from "@tanstack/react-query";
import { useMemo, useRef, useState } from "react";
import { Pressable, StyleSheet, View } from "react-native";
import { errorMessage } from "../../lib/api/errors";
import type { NotebookSource, ReportTemplate, StudioKind } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import { type BillingAction, billingActionFor } from "../../lib/billing";
import { hasMessage, LOCALE_NAMES, type Locale } from "../../lib/i18n/translate";
import { keys, usePodcastLanguages, useReportTemplates, useSuggestions } from "../../lib/queries";
import { CUSTOM_STYLE_MAX_CHARS, studioTile } from "../../lib/studio/catalog";
import {
	appendSuggestion,
	buildStudioRequest,
	initialDialogState,
	needsStyleDescription,
	newRequestId,
	STUDIO_LANGUAGES,
	type StudioDialogState,
	type StudioRequestBody,
	suggestionVariant,
	suggestionWindow,
	windowCount,
} from "../../lib/studio/request";
import { radius, space } from "../../theme/colors";
import { useBillingNoticeAction } from "../billing-link";
import { Sheet } from "../ui/layout";
import {
	AppText,
	Button,
	Checkbox,
	Chip,
	DelayedSpinner,
	Icon,
	Notice,
	OptionCard,
	Segmented,
	TextField,
} from "../ui/primitives";
import { tileIcon } from "./tile-icon";

/** Which group of three ideas each open starts on (the web keeps this per browser). */
const openCounters = new Map<string, number>();
function claimOffset(key: string): number {
	const current = openCounters.get(key) ?? 0;
	openCounters.set(key, current + 1);
	return current;
}

/** English names for tags the app has no translation of; the six UI languages name themselves. */
const LANGUAGE_LABELS: Record<string, string> = {
	fr: "Français",
	de: "Deutsch",
	it: "Italiano",
	ja: "日本語",
	ar: "العربية",
	ru: "Русский",
};
function languageLabel(tag: string): string {
	const base = tag.toLowerCase().split(/[-_]/)[0];
	return LOCALE_NAMES[base as Locale] ?? LANGUAGE_LABELS[base] ?? tag;
}

/**
 * The Studio generate dialog as a sheet, with the web's options: described
 * cards, one-line pills with "Default: X" beside the label, report templates,
 * language, the sources to use, and a focus box with three rotating ideas.
 */
export function GenerateSheet({
	kind,
	onClose,
	workspaceId,
	notebookId,
	notebookTitle,
	sources,
	onGenerate,
	onAddSources,
}: {
	kind: StudioKind | null;
	onClose: () => void;
	workspaceId: number;
	notebookId: number;
	notebookTitle: string;
	sources: NotebookSource[];
	onGenerate: (body: StudioRequestBody) => Promise<void>;
	onAddSources: () => void;
}) {
	const { t } = useApp();
	const title = kind ? t(`notebooks.kinds.${kind}.dialog_title`) : undefined;
	return (
		<Sheet visible={kind !== null} onClose={onClose} title={title} full={sources.length > 0}>
			{kind === null ? null : sources.length === 0 ? (
				<View style={{ gap: space.md }}>
					<AppText tone="muted">
						{t("notebooks.needs_sources.body", { output: t(`notebooks.kinds.${kind}.label`) })}
					</AppText>
					<Button kind="outline" icon="add" title={t("notebooks.add_sources")} onPress={onAddSources} />
				</View>
			) : (
				<GenerateForm
					key={kind}
					kind={kind}
					onClose={onClose}
					workspaceId={workspaceId}
					notebookId={notebookId}
					notebookTitle={notebookTitle}
					sources={sources}
					onGenerate={onGenerate}
				/>
			)}
		</Sheet>
	);
}

function GenerateForm({
	kind,
	onClose,
	workspaceId,
	notebookId,
	notebookTitle,
	sources,
	onGenerate,
}: {
	kind: StudioKind;
	onClose: () => void;
	workspaceId: number;
	notebookId: number;
	notebookTitle: string;
	sources: NotebookSource[];
	onGenerate: (body: StudioRequestBody) => Promise<void>;
}) {
	const { t, locale, ep, colors } = useApp();
	const queryClient = useQueryClient();
	const tile = studioTile(kind);
	const enabledIds = useMemo(() => sources.filter((s) => s.enabled).map((s) => s.id), [sources]);
	const [state, setState] = useState<StudioDialogState>(() => initialDialogState(kind, enabledIds));
	const [view, setView] = useState<"options" | "sources" | "language">("options");
	const [submitting, setSubmitting] = useState(false);
	const [error, setErrorState] = useState<{ message: string; billing: BillingAction | null } | null>(null);
	const setError = (message: string | null, cause?: unknown) =>
		setErrorState(message === null ? null : { message, billing: billingActionFor(cause) });
	const billingNotice = useBillingNoticeAction();
	const [requestId] = useState(newRequestId);

	const variant = suggestionVariant(kind, state.options);
	const suggestions = useSuggestions(
		workspaceId,
		notebookId,
		kind,
		{ variant, language: state.language },
		state.sourceIds.length > 0
	);
	const rotationKey = `${notebookId}:${kind}:${variant}:${state.language}`;
	const [rotation, setRotation] = useState(() => ({ key: rotationKey, offset: claimOffset(rotationKey), seen: 1 }));
	if (rotation.key !== rotationKey) {
		setRotation({ key: rotationKey, offset: claimOffset(rotationKey), seen: 1 });
	}
	const [refreshingIdeas, setRefreshingIdeas] = useState(false);
	const refreshingRef = useRef(false);

	const pool = suggestions.data?.pool.length ? suggestions.data.pool : (suggestions.data?.suggestions ?? []);
	const examples = suggestions.data?.examples.length
		? suggestions.data.examples
		: suggestions.data?.placeholder
			? [suggestions.data.placeholder]
			: [];
	const { suggestions: ideas, placeholder } = suggestionWindow(pool, examples, rotation.offset);

	const moreIdeas = async () => {
		if (refreshingRef.current) return;
		if (rotation.seen < windowCount(pool.length)) {
			setRotation((r) => ({ ...r, offset: r.offset + 1, seen: r.seen + 1 }));
			return;
		}
		refreshingRef.current = true;
		setRefreshingIdeas(true);
		try {
			const fresh = await ep.suggestions(workspaceId, notebookId, kind, {
				variant,
				language: state.language,
				refresh: true,
			});
			queryClient.setQueryData(keys.suggestions(workspaceId, notebookId, kind, variant, state.language), fresh);
			setRotation((r) => ({ ...r, offset: 0, seen: 1 }));
		} catch {
			setError(t("notebooks.more_ideas_failed"));
		} finally {
			refreshingRef.current = false;
			setRefreshingIdeas(false);
		}
	};

	const templates = useReportTemplates(workspaceId, kind === "report");
	const reportFormat = state.options.format;
	const visibleTemplates = (templates.data ?? []).filter((template) => template.format === reportFormat);
	// The first template of the chosen report format is preselected, as on the web;
	// an explicit pick (or un-pick) of this format's template wins.
	const [templatePick, setTemplatePick] = useState<{ format: string; key: string | null } | null>(null);
	const pickedKey =
		templatePick && templatePick.format === reportFormat ? templatePick.key : (visibleTemplates[0]?.key ?? null);
	const pickedTemplate = visibleTemplates.find((template) => template.key === pickedKey) ?? null;

	const podcastLanguages = usePodcastLanguages(kind === "audio");
	const languages = kind === "audio" ? (podcastLanguages.data?.languages ?? []) : STUDIO_LANGUAGES;

	const setOption = (field: string, value: string) =>
		setState((s) => ({ ...s, options: { ...s.options, [field]: value } }));
	const templateTitle = (template: ReportTemplate) =>
		!template.customized && hasMessage(locale, `notebooks.templates.${template.key}.title`)
			? t(`notebooks.templates.${template.key}.title`)
			: template.title;
	const templateDescription = (template: ReportTemplate) =>
		!template.customized && hasMessage(locale, `notebooks.templates.${template.key}.description`)
			? t(`notebooks.templates.${template.key}.description`)
			: template.description;

	const submit = async () => {
		if (state.sourceIds.length === 0) {
			setError(t("notebooks.choose_sources"));
			return;
		}
		setSubmitting(true);
		setError(null);
		try {
			await onGenerate(
				buildStudioRequest(
					kind === "report"
						? { ...state, templateKey: pickedTemplate?.key ?? null, templatePrompt: pickedTemplate?.prompt ?? null }
						: state,
					{
						enabledSourceIds: enabledIds,
						title: `${t(`notebooks.kinds.${kind}.label`)}: ${notebookTitle}`,
						requestId,
					}
				)
			);
			onClose();
		} catch (e) {
			setError(errorMessage(e, t("notebooks.generate_failed")), e);
			setSubmitting(false);
		}
	};

	if (view === "sources") {
		return (
			<SourcePicker
				sources={sources}
				selected={state.sourceIds}
				onDone={(ids) => {
					setState((s) => ({ ...s, sourceIds: ids }));
					setView("options");
				}}
			/>
		);
	}

	if (view === "language") {
		return (
			<View style={{ gap: space.xs }}>
				{["", ...languages].map((tag) => {
					const selected = state.language === tag;
					return (
						<Pressable
							key={tag || "default"}
							onPress={() => {
								setState((s) => ({ ...s, language: tag }));
								setView("options");
							}}
							accessibilityRole="radio"
							accessibilityState={{ selected }}
							style={[styles.listRow, { borderColor: selected ? colors.foreground : colors.border }]}
						>
							<AppText style={{ flex: 1 }}>{tag ? languageLabel(tag) : t("notebooks.language_default")}</AppText>
							{selected ? <Icon name="checkmark" /> : null}
						</Pressable>
					);
				})}
			</View>
		);
	}

	return (
		<>
			<View style={styles.lead}>
				<Icon name={tileIcon(kind)} size={20} color={colors.mutedForeground} />
				<AppText variant="small" tone="muted" style={{ flex: 1 }}>
					{t(`notebooks.kinds.${kind}.description`)}
				</AppText>
			</View>

			{tile.groups.map((group) => {
				if (kind === "report" && group.field === "file_type" && reportFormat !== "document") return null;
				const label = t(`notebooks.fields.${group.field}`);
				const valueLabel = (value: string) => t(`notebooks.options.${kind}.${group.field}.${value}.label`);
				const describe = (value: string) =>
					hasMessage(locale, `notebooks.options.${kind}.${group.field}.${value}.description`)
						? t(`notebooks.options.${kind}.${group.field}.${value}.description`)
						: undefined;
				return (
					<View key={group.field} style={{ gap: space.sm }}>
						<View style={styles.labelRow}>
							<AppText variant="label">{label}</AppText>
							{group.presentation === "segments" ? (
								<AppText variant="caption" tone="muted">
									{t("notebooks.default_is", { value: valueLabel(group.defaultValue) })}
								</AppText>
							) : null}
						</View>
						{group.presentation === "segments" ? (
							<Segmented
								accessibilityLabel={label}
								value={state.options[group.field]}
								onChange={(value) => setOption(group.field, value)}
								options={group.values.map((value) => ({
									value,
									label: valueLabel(value),
									accessibilityLabel:
										value === group.defaultValue
											? `${valueLabel(value)} ${t("notebooks.default_suffix")}`
											: valueLabel(value),
								}))}
							/>
						) : (
							<View style={{ gap: space.sm }}>
								{group.values.map((value) => (
									<OptionCard
										key={value}
										title={
											value === group.defaultValue
												? `${valueLabel(value)} ${t("notebooks.default_suffix")}`
												: valueLabel(value)
										}
										description={describe(value)}
										selected={state.options[group.field] === value}
										onPress={() => setOption(group.field, value)}
									/>
								))}
							</View>
						)}
						{kind === "video" && group.field === "visual_style" && state.options.visual_style === "custom" ? (
							<TextField
								value={state.stylePrompt ?? ""}
								onChangeText={(text) => setState((s) => ({ ...s, stylePrompt: text }))}
								placeholder={t("notebooks.custom_style_placeholder")}
								accessibilityLabel={t("notebooks.custom_style_label")}
								maxLength={CUSTOM_STYLE_MAX_CHARS}
								multiline
							/>
						) : null}
					</View>
				);
			})}

			{kind === "report" ? (
				<View style={{ gap: space.sm }}>
					<AppText variant="label">{t("notebooks.fields.template")}</AppText>
					{templates.isLoading ? <DelayedSpinner active /> : null}
					{visibleTemplates.map((template) => (
						<OptionCard
							key={template.key}
							title={templateTitle(template)}
							description={templateDescription(template)}
							selected={pickedKey === template.key}
							onPress={() =>
								setTemplatePick({ format: reportFormat, key: pickedKey === template.key ? null : template.key })
							}
						/>
					))}
				</View>
			) : null}

			<View style={styles.pickers}>
				{tile.language ? (
					<View style={{ flex: 1, gap: space.xs }}>
						<AppText variant="label">{t("notebooks.fields.language")}</AppText>
						<Pressable
							onPress={() => setView("language")}
							accessibilityRole="button"
							style={[styles.picker, { borderColor: colors.input }]}
						>
							<AppText numberOfLines={1} style={{ flex: 1 }}>
								{state.language ? languageLabel(state.language) : t("notebooks.language_default")}
							</AppText>
							<Icon name="chevron-forward" size={16} color={colors.mutedForeground} />
						</Pressable>
					</View>
				) : null}
				<View style={{ flex: 1, gap: space.xs }}>
					<AppText variant="label">{t("notebooks.sources")}</AppText>
					<Pressable
						onPress={() => setView("sources")}
						accessibilityRole="button"
						style={[styles.picker, { borderColor: colors.input }]}
					>
						<AppText numberOfLines={1} style={{ flex: 1 }}>
							{t("notebooks.source_count", { count: state.sourceIds.length })}
						</AppText>
						<Icon name="chevron-forward" size={16} color={colors.mutedForeground} />
					</Pressable>
				</View>
			</View>

			<View style={{ gap: space.sm }}>
				<AppText variant="label">{t(`notebooks.kinds.${kind}.focus_label`)}</AppText>
				<TextField
					value={state.focus}
					onChangeText={(text) => setState((s) => ({ ...s, focus: text }))}
					placeholder={
						placeholder
							? t("notebooks.focus_example", { example: placeholder })
							: t(`notebooks.kinds.${kind}.focus_placeholder`)
					}
					accessibilityLabel={t(`notebooks.kinds.${kind}.focus_label`)}
					multiline
					maxLength={2000}
				/>
				{ideas.length > 0 ? (
					<>
						<View style={styles.labelRow}>
							<AppText variant="caption" tone="muted">
								{t(`notebooks.kinds.${kind}.ideas_label`)}
							</AppText>
							<Pressable
								onPress={moreIdeas}
								disabled={refreshingIdeas}
								accessibilityRole="button"
								hitSlop={8}
								style={{ flexDirection: "row", alignItems: "center", gap: 4 }}
							>
								<Icon name="refresh" size={14} color={colors.mutedForeground} />
								<AppText variant="caption" tone="muted">
									{t("notebooks.more_ideas")}
								</AppText>
							</Pressable>
						</View>
						<View style={{ gap: space.sm }}>
							{ideas.map((idea) => (
								<Chip
									key={idea}
									icon="sparkles-outline"
									label={idea}
									onPress={() =>
										setState((s) => ({
											...s,
											focus: kind === "video" ? idea : appendSuggestion(s.focus, idea),
										}))
									}
								/>
							))}
						</View>
					</>
				) : suggestions.isLoading && state.sourceIds.length > 0 ? (
					<DelayedSpinner active />
				) : null}
			</View>

			{error ? <Notice tone="error" message={error.message} {...billingNotice(error.billing)} /> : null}

			<View style={styles.footer}>
				<Button kind="ghost" title={t("notebooks.cancel")} onPress={onClose} />
				<Button
					title={t("notebooks.generate_now")}
					icon="sparkles"
					busy={submitting}
					disabled={state.sourceIds.length === 0 || needsStyleDescription(state)}
					onPress={submit}
				/>
			</View>
		</>
	);
}

function SourcePicker({
	sources,
	selected,
	onDone,
}: {
	sources: NotebookSource[];
	selected: number[];
	onDone: (ids: number[]) => void;
}) {
	const { t } = useApp();
	const [ids, setIds] = useState<number[]>(selected);
	const all = ids.length === sources.length;
	return (
		<View style={{ gap: space.sm }}>
			<View style={styles.labelRow}>
				<AppText variant="label">{t("mobile.sources_picker")}</AppText>
				<Checkbox
					checked={all}
					label={t("notebooks.select_all")}
					onPress={() => setIds(all ? [] : sources.map((s) => s.id))}
				/>
			</View>
			{sources.map((source) => {
				const checked = ids.includes(source.id);
				return (
					<Pressable
						key={source.id}
						onPress={() =>
							setIds((current) => (checked ? current.filter((id) => id !== source.id) : [...current, source.id]))
						}
						accessibilityRole="checkbox"
						accessibilityState={{ checked }}
						style={styles.sourceRow}
					>
						<AppText numberOfLines={2} style={{ flex: 1 }}>
							{source.title}
						</AppText>
						<Icon name={checked ? "checkbox" : "square-outline"} />
					</Pressable>
				);
			})}
			<View style={styles.footer}>
				<Button title={t("mobile.done")} disabled={ids.length === 0} onPress={() => onDone(ids)} />
			</View>
		</View>
	);
}

const styles = StyleSheet.create({
	lead: { flexDirection: "row", alignItems: "center", gap: space.sm },
	labelRow: { flexDirection: "row", alignItems: "baseline", justifyContent: "space-between", gap: space.md },
	pickers: { flexDirection: "row", gap: space.md },
	picker: {
		flexDirection: "row",
		alignItems: "center",
		borderWidth: 1,
		borderRadius: radius.md,
		minHeight: 44,
		paddingHorizontal: space.md,
		gap: space.sm,
	},
	listRow: {
		flexDirection: "row",
		alignItems: "center",
		borderWidth: 1,
		borderRadius: radius.md,
		minHeight: 48,
		paddingHorizontal: space.md,
	},
	sourceRow: { flexDirection: "row", alignItems: "center", gap: space.md, minHeight: 48 },
	footer: { flexDirection: "row", gap: space.sm, justifyContent: "flex-end", paddingTop: space.sm },
});
