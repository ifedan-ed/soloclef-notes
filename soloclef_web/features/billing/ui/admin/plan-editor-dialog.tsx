"use client";

import { useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { billingAdminApi } from "../../api/billing-admin-api";
import type { AdminOverview, AdminPlan, PlanBody } from "../../api/billing-admin-schemas";
import { useAdminMutation } from "../../hooks/use-billing-admin";
import {
	type ContentLocale,
	draftToPlanBody,
	emptyPlanDraft,
	type PlanDraft,
	planDraftProblems,
	planToDraft,
	toggle,
} from "../../lib/admin-forms";
import { LocalePicker } from "./locale-picker";

/** Create or edit a plan: price, credits, entitlements and per-language content. */
export function PlanEditorDialog({
	plan,
	choices,
	nextSortOrder,
	onClose,
}: {
	plan: AdminPlan | "new" | null;
	choices: AdminOverview["choices"];
	nextSortOrder: number;
	onClose: () => void;
}) {
	return (
		<Dialog open={plan !== null} onOpenChange={(open) => !open && onClose()}>
			<DialogContent className="flex max-h-[90vh] w-[calc(100vw-2rem)] max-w-2xl flex-col gap-4 overflow-y-auto">
				{plan ? (
					<PlanEditor
						key={plan === "new" ? "new" : plan.id}
						plan={plan}
						choices={choices}
						nextSortOrder={nextSortOrder}
						onClose={onClose}
					/>
				) : null}
			</DialogContent>
		</Dialog>
	);
}

function NumberField({
	id,
	label,
	value,
	onChange,
	hint,
	decimal = false,
}: {
	id: string;
	label: string;
	value: string;
	onChange: (value: string) => void;
	hint?: string;
	decimal?: boolean;
}) {
	return (
		<div className="space-y-1">
			<label htmlFor={id} className="text-sm font-medium">
				{label}
			</label>
			<Input
				id={id}
				inputMode={decimal ? "decimal" : "numeric"}
				value={value}
				onChange={(e) => onChange(e.target.value)}
			/>
			{hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
		</div>
	);
}

function CheckboxGroup({
	legend,
	name,
	options,
	selected,
	onChange,
	labelFor,
}: {
	legend: string;
	name: string;
	options: string[];
	selected: string[];
	onChange: (next: string[]) => void;
	labelFor: (value: string) => string;
}) {
	return (
		<fieldset className="space-y-2">
			<legend className="text-sm font-medium">{legend}</legend>
			<div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
				{options.map((value) => (
					<div key={value} className="flex items-center gap-2 text-sm">
						<Checkbox
							id={`${name}-${value}`}
							checked={selected.includes(value)}
							onCheckedChange={(on) => onChange(toggle(selected, value, on === true))}
						/>
						<label htmlFor={`${name}-${value}`} className="min-w-0 break-words">
							{labelFor(value)}
						</label>
					</div>
				))}
			</div>
		</fieldset>
	);
}

function PlanEditor({
	plan,
	choices,
	nextSortOrder,
	onClose,
}: {
	plan: AdminPlan | "new";
	choices: AdminOverview["choices"];
	nextSortOrder: number;
	onClose: () => void;
}) {
	const t = useTranslations("billing");
	const tn = useTranslations("notebooks");
	const [draft, setDraft] = useState<PlanDraft>(() =>
		plan === "new" ? emptyPlanDraft(nextSortOrder) : planToDraft(plan)
	);
	const [contentLocale, setContentLocale] = useState<ContentLocale>("en");
	const save = useAdminMutation(
		(body: PlanBody) =>
			plan === "new" ? billingAdminApi.createPlan(body) : billingAdminApi.updatePlan(plan.id, body),
		{ onSuccess: onClose }
	);
	const set = <K extends keyof PlanDraft>(key: K, value: PlanDraft[K]) =>
		setDraft((d) => ({ ...d, [key]: value }));
	const content = draft.content[contentLocale];
	const setContent = (field: "name" | "tagline" | "features", value: string) =>
		setDraft((d) => ({
			...d,
			content: { ...d.content, [contentLocale]: { ...d.content[contentLocale], [field]: value } },
		}));
	const problems = planDraftProblems(draft);
	const kindLabel = (kind: string) =>
		tn.has(`kinds.${kind}.label`) ? tn(`kinds.${kind}.label`) : kind;

	return (
		<>
			<DialogHeader>
				<DialogTitle>{plan === "new" ? t("admin.new_plan") : t("admin.edit_plan")}</DialogTitle>
				<DialogDescription>{t("admin.plan_editor_description")}</DialogDescription>
			</DialogHeader>

			<div className="grid gap-3 sm:grid-cols-3">
				<div className="space-y-1">
					<label htmlFor="plan-slug" className="text-sm font-medium">
						{t("admin.slug")}
					</label>
					<Input
						id="plan-slug"
						value={draft.slug}
						onChange={(e) => set("slug", e.target.value)}
						aria-invalid={problems.includes("slug")}
						autoComplete="off"
					/>
				</div>
				<NumberField
					id="plan-price"
					label={t("admin.price_monthly")}
					value={draft.price}
					onChange={(v) => set("price", v)}
					decimal
				/>
				<NumberField
					id="plan-credits"
					label={t("admin.monthly_credits")}
					value={draft.monthly_credits}
					onChange={(v) => set("monthly_credits", v)}
					decimal
				/>
				<div className="space-y-1">
					<label htmlFor="plan-currency" className="text-sm font-medium">
						{t("admin.currency")}
					</label>
					<Input
						id="plan-currency"
						value={draft.currency}
						onChange={(e) => set("currency", e.target.value)}
						autoComplete="off"
					/>
				</div>
				<NumberField
					id="plan-sort"
					label={t("admin.sort_order")}
					value={draft.sort_order}
					onChange={(v) => set("sort_order", v)}
				/>
			</div>

			<div className="grid gap-3 sm:grid-cols-3">
				{(["public", "active", "is_default"] as const).map((key) => (
					<div key={key} className="flex items-center justify-between gap-2 rounded-md border p-2">
						<span className="text-sm">{t(`admin.flags.${key}`)}</span>
						<Switch
							checked={draft[key]}
							onCheckedChange={(v) => set(key, v)}
							aria-label={t(`admin.flags.${key}`)}
						/>
					</div>
				))}
			</div>

			<section className="space-y-3 rounded-lg border p-3">
				<h3 className="text-sm font-semibold">{t("admin.entitlements")}</h3>
				<CheckboxGroup
					legend={t("admin.parsers_label")}
					name="plan-parser"
					options={choices.parsers}
					selected={draft.parsers}
					onChange={(v) => set("parsers", v)}
					labelFor={(p) => (t.has(`admin.parsers.${p}`) ? t(`admin.parsers.${p}`) : p)}
				/>
				<div className="flex items-center justify-between gap-2">
					<span className="text-sm font-medium">{t("admin.vision")}</span>
					<Switch
						checked={draft.vision}
						onCheckedChange={(v) => set("vision", v)}
						aria-label={t("admin.vision")}
					/>
				</div>
				<CheckboxGroup
					legend={t("admin.model_tiers_label")}
					name="plan-tier"
					options={choices.model_tiers}
					selected={draft.model_tiers}
					onChange={(v) => set("model_tiers", v)}
					labelFor={(tier) => (t.has(`admin.tiers.${tier}`) ? t(`admin.tiers.${tier}`) : tier)}
				/>
				<CheckboxGroup
					legend={t("admin.studio_kinds_label")}
					name="plan-kind"
					options={choices.studio_kinds}
					selected={draft.studio_kinds}
					onChange={(v) => set("studio_kinds", v)}
					labelFor={kindLabel}
				/>
				<div className="grid gap-3 sm:grid-cols-2">
					<NumberField
						id="plan-video"
						label={t("admin.max_video_seconds")}
						value={draft.max_video_seconds}
						onChange={(v) => set("max_video_seconds", v)}
					/>
					<NumberField
						id="plan-pages"
						label={t("admin.max_pages_per_upload")}
						value={draft.max_pages_per_upload}
						onChange={(v) => set("max_pages_per_upload", v)}
						hint={t("admin.zero_unlimited")}
					/>
					<NumberField
						id="plan-jobs"
						label={t("admin.max_concurrent_jobs")}
						value={draft.max_concurrent_jobs}
						onChange={(v) => set("max_concurrent_jobs", v)}
						hint={t("admin.zero_unlimited")}
					/>
					<NumberField
						id="plan-notebooks"
						label={t("admin.max_notebooks")}
						value={draft.max_notebooks}
						onChange={(v) => set("max_notebooks", v)}
						hint={t("admin.zero_unlimited")}
					/>
					<NumberField
						id="plan-sources"
						label={t("admin.max_sources_per_notebook")}
						value={draft.max_sources_per_notebook}
						onChange={(v) => set("max_sources_per_notebook", v)}
						hint={t("admin.zero_unlimited")}
					/>
				</div>
			</section>

			<section className="space-y-3 rounded-lg border p-3">
				<h3 className="text-sm font-semibold">{t("admin.content")}</h3>
				<LocalePicker value={contentLocale} onChange={setContentLocale} />
				<div className="space-y-1">
					<label htmlFor="plan-name" className="text-sm font-medium">
						{t("admin.plan_name")}
					</label>
					<Input
						id="plan-name"
						value={content.name}
						onChange={(e) => setContent("name", e.target.value)}
						aria-invalid={contentLocale === "en" && problems.includes("name_en")}
					/>
				</div>
				<div className="space-y-1">
					<label htmlFor="plan-tagline" className="text-sm font-medium">
						{t("admin.tagline")}
					</label>
					<Input
						id="plan-tagline"
						value={content.tagline}
						onChange={(e) => setContent("tagline", e.target.value)}
					/>
				</div>
				<div className="space-y-1">
					<label htmlFor="plan-features" className="text-sm font-medium">
						{t("admin.features")}
					</label>
					<Textarea
						id="plan-features"
						rows={5}
						value={content.features}
						onChange={(e) => setContent("features", e.target.value)}
					/>
					<p className="text-xs text-muted-foreground">{t("admin.features_hint")}</p>
				</div>
			</section>

			{problems.length > 0 ? (
				<p className="text-sm text-destructive">
					{problems.map((p) => t(`admin.problems.${p}`)).join(" ")}
				</p>
			) : null}

			<DialogFooter>
				<Button variant="ghost" onClick={onClose}>
					{t("cancel")}
				</Button>
				<Button
					disabled={problems.length > 0 || save.isPending}
					onClick={() => save.mutate(draftToPlanBody(draft))}
				>
					{t("admin.save")}
				</Button>
			</DialogFooter>
		</>
	);
}
