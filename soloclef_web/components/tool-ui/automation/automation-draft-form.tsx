"use client";

import { ChevronRight, FolderInput } from "lucide-react";
import { useId, useMemo, useState } from "react";
import { AutomationChatModelField } from "@/app/dashboard/automations/components/builder/automation-model-fields";
import { Field } from "@/app/dashboard/automations/components/builder/form-field";
import { ScheduleSection } from "@/app/dashboard/automations/components/builder/schedule-section";
import { UnattendedToggle } from "@/app/dashboard/automations/components/builder/unattended-toggle";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import type { AutomationDraft } from "@/contracts/types/automation-draft.types";
import { useNotebooks } from "@/features/notebooks/hooks/use-notebook-queries";
import { type BuilderSchedule, scheduleToCron } from "@/lib/automations/builder-schema";
import {
	type DraftFields,
	type DraftReach,
	draftFieldProblems,
} from "@/lib/automations/draft-fields";
import {
	ANSWER_FROM,
	CAN_USE_LABEL,
	DELETED_NOTEBOOK,
	EMAIL_RESULT_LABEL,
	REACH_OPTIONS,
	reachOption,
	SAVE_RESULT_TO,
} from "@/lib/automations/reach-wording";
import { fromCron } from "@/lib/automations/schedule-builder";

const NONE = "none";

/** The builder's schedule editor's model of a drafted cron: its simple picker when it fits. */
export function scheduleOf(cron: string): BuilderSchedule {
	const model = fromCron(cron);
	return model ? { mode: "preset", model } : { mode: "cron", cron };
}

/**
 * The card's fields as the person has them, with the schedule editor's
 * state folded in; null while the schedule was removed in the editor.
 */
function current(
	fields: DraftFields,
	schedule: BuilderSchedule | null,
	timezone: string
): DraftFields | null {
	if (fields.trigger.kind !== "schedule") return fields;
	if (schedule === null) return null;
	return { ...fields, trigger: { kind: "schedule", cron: scheduleToCron(schedule), timezone } };
}

interface DraftFormProps {
	draft: AutomationDraft;
	initial: DraftFields;
	disabled: boolean;
	/** Told every change: the fields to create with, and what stops Create (in words). */
	onChange: (fields: DraftFields | null, problems: string[]) => void;
}

/**
 * Every field of a drafted automation, editable: what the person changes
 * here is what Create sends. Built from the builder's own pieces (its
 * schedule editor, time zone picker, model field and approvals switch), so
 * a draft is edited the way an automation is.
 */
export function AutomationDraftForm({ draft, initial, disabled, onChange }: DraftFormProps) {
	const ids = {
		name: useId(),
		task: useId(),
		reach: useId(),
		only: useId(),
		save: useId(),
		about: useId(),
	};
	const { data: notebooks } = useNotebooks();
	const [fields, setFields] = useState<DraftFields>(initial);
	const [schedule, setSchedule] = useState<BuilderSchedule | null>(
		initial.trigger.kind === "schedule" ? scheduleOf(initial.trigger.cron) : null
	);
	const [timezone, setTimezone] = useState(
		initial.trigger.kind === "schedule" ? initial.trigger.timezone : ""
	);

	const report = (
		nextFields: DraftFields,
		nextSchedule: BuilderSchedule | null,
		nextZone: string
	) => {
		const ready = current(nextFields, nextSchedule, nextZone);
		onChange(ready, ready ? draftFieldProblems(ready) : ["Choose when it runs."]);
	};
	const patch = (change: Partial<DraftFields>) => {
		const next = { ...fields, ...change };
		setFields(next);
		report(next, schedule, timezone);
	};

	const reach = reachOption(fields.reach);
	const folder = useMemo(
		() =>
			fields.trigger.kind === "event"
				? (draft.folders.find((f) => f.id === (fields.trigger as { folderId: number }).folderId)
						?.title ?? "a folder that no longer exists")
				: null,
		[draft.folders, fields.trigger]
	);
	const notebookOptions = notebooks ?? [];
	const missing = (id: number | null) => id !== null && !notebookOptions.some((n) => n.id === id);

	return (
		<fieldset disabled={disabled} className="space-y-4" data-testid="automation-draft-form">
			<Field label="Name" htmlFor={ids.name}>
				<Input
					id={ids.name}
					value={fields.name}
					maxLength={200}
					onChange={(event) => patch({ name: event.target.value })}
				/>
			</Field>
			<Field label="What it does" htmlFor={ids.task} hint="Asked of the assistant at every run.">
				<Textarea
					id={ids.task}
					value={fields.task}
					rows={3}
					className="min-h-20 resize-y"
					onChange={(event) => patch({ task: event.target.value })}
				/>
			</Field>

			<div className="space-y-1.5">
				<p className="text-xs font-medium text-foreground">When it runs</p>
				{fields.trigger.kind === "schedule" ? (
					<ScheduleSection
						schedule={schedule}
						timezone={timezone}
						errors={{}}
						onScheduleChange={(next) => {
							setSchedule(next);
							report(fields, next, timezone);
						}}
						onTimezoneChange={(next) => {
							setTimezone(next);
							report(fields, schedule, next);
						}}
					/>
				) : (
					<div className="flex items-start gap-2 rounded-md border border-border/60 bg-accent px-3 py-2 text-sm">
						<FolderInput className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" aria-hidden />
						<div className="min-w-0">
							<p className="text-foreground">
								When a document arrives in <span className="font-medium">{folder}</span>
							</p>
							<p className="text-xs text-muted-foreground">
								To wait on another folder, ask in the chat.
							</p>
						</div>
					</div>
				)}
			</div>

			<div className="grid gap-3 sm:grid-cols-2">
				<Field label={CAN_USE_LABEL} htmlFor={ids.reach} hint={reach.hint}>
					<Select
						value={fields.reach}
						onValueChange={(value) => patch({ reach: value as DraftReach })}
					>
						<SelectTrigger id={ids.reach} className="w-full">
							<SelectValue />
						</SelectTrigger>
						<SelectContent>
							{REACH_OPTIONS.map((option) => (
								<SelectItem key={option.value} value={option.value}>
									{option.label}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</Field>
				<Field
					label={ANSWER_FROM.label}
					htmlFor={ids.only}
					hint={ANSWER_FROM.hint(fields.notebookId !== null)}
				>
					<Select
						value={fields.notebookId === null ? NONE : String(fields.notebookId)}
						onValueChange={(value) => patch({ notebookId: value === NONE ? null : Number(value) })}
					>
						<SelectTrigger id={ids.only} className="w-full">
							<SelectValue />
						</SelectTrigger>
						<SelectContent>
							<SelectItem value={NONE}>{ANSWER_FROM.any}</SelectItem>
							{notebookOptions.map((notebook) => (
								<SelectItem key={notebook.id} value={String(notebook.id)}>
									{notebook.title}
								</SelectItem>
							))}
							{missing(fields.notebookId) ? (
								<SelectItem value={String(fields.notebookId)}>{DELETED_NOTEBOOK}</SelectItem>
							) : null}
						</SelectContent>
					</Select>
				</Field>
			</div>

			<Field
				label={SAVE_RESULT_TO.label}
				htmlFor={ids.save}
				hint={SAVE_RESULT_TO.hint(fields.saveToNotebookId !== null)}
			>
				<Select
					value={fields.saveToNotebookId === null ? NONE : String(fields.saveToNotebookId)}
					onValueChange={(value) =>
						patch({ saveToNotebookId: value === NONE ? null : Number(value) })
					}
				>
					<SelectTrigger id={ids.save} className="w-full">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value={NONE}>{SAVE_RESULT_TO.none}</SelectItem>
						{notebookOptions.map((notebook) => (
							<SelectItem key={notebook.id} value={String(notebook.id)}>
								{notebook.title}
							</SelectItem>
						))}
						{missing(fields.saveToNotebookId) ? (
							<SelectItem value={String(fields.saveToNotebookId)}>{DELETED_NOTEBOOK}</SelectItem>
						) : null}
					</SelectContent>
				</Select>
			</Field>

			<div className="flex items-start justify-between gap-3">
				<div className="min-w-0 space-y-0.5">
					<p className="text-sm font-medium text-foreground">{EMAIL_RESULT_LABEL}</p>
					<p className="text-xs text-muted-foreground">
						Sent to your account's address when a run ends.
					</p>
				</div>
				<Switch
					checked={fields.emailResult}
					onCheckedChange={(checked) => patch({ emailResult: checked })}
					aria-label={EMAIL_RESULT_LABEL}
				/>
			</div>

			{/* Settings most drafts keep as they are, folded so the card reads
			    as what it does and when; a model the person named is shown
			    open, so a choice the chat made is never hidden. */}
			<Collapsible defaultOpen={initial.model !== null}>
				<CollapsibleTrigger className="group flex items-center gap-1 text-xs font-medium text-muted-foreground hover:text-foreground">
					<ChevronRight
						className="h-3.5 w-3.5 transition-transform group-data-[state=open]:rotate-90"
						aria-hidden
					/>
					More settings
				</CollapsibleTrigger>
				<CollapsibleContent className="space-y-4 pt-3">
					<AutomationChatModelField value={fields.model} onChange={(model) => patch({ model })} />
					<UnattendedToggle
						checked={fields.unattended}
						onChange={(unattended) => patch({ unattended })}
					/>
					<Field label="Description" htmlFor={ids.about} hint="Shown in your Automations list.">
						<Input
							id={ids.about}
							value={fields.description}
							onChange={(event) => patch({ description: event.target.value })}
						/>
					</Field>
				</CollapsibleContent>
			</Collapsible>
		</fieldset>
	);
}
