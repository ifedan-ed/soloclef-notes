"use client";

import { useId } from "react";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { useNotebooks } from "@/features/notebooks/hooks/use-notebook-queries";
import type { BuilderTask, TaskScope } from "@/lib/automations/builder-schema";
import {
	ANSWER_FROM,
	CAN_USE_LABEL,
	DELETED_NOTEBOOK,
	REACH_OPTIONS,
	reachOption,
	SAVE_RESULT_TO,
} from "@/lib/automations/reach-wording";
import { Field } from "./form-field";

const ANY_NOTEBOOK = "any";
const DONT_SAVE = "none";

interface TaskReachFieldsProps {
	task: BuilderTask;
	onChange: (patch: Partial<BuilderTask>) => void;
}

/** What one automation task may read: the web or not, and which notebook. */
export function TaskReachFields({ task, onChange }: TaskReachFieldsProps) {
	const scopeId = useId();
	const notebookId = useId();
	const saveToNotebookId = useId();
	const { data: notebooks } = useNotebooks();
	const scope = reachOption(task.scope);
	// A notebook that was deleted still shows as picked until changed.
	const pickedMissing =
		task.notebookId !== null && !(notebooks ?? []).some((n) => n.id === task.notebookId);
	const saveTargetMissing =
		task.saveToNotebookId !== null &&
		!(notebooks ?? []).some((n) => n.id === task.saveToNotebookId);

	return (
		<div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
			<Field label={CAN_USE_LABEL} htmlFor={scopeId} hint={scope.hint}>
				<Select value={task.scope} onValueChange={(v) => onChange({ scope: v as TaskScope })}>
					<SelectTrigger id={scopeId} aria-label={CAN_USE_LABEL} className="w-full">
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
				htmlFor={notebookId}
				hint={ANSWER_FROM.hint(task.notebookId !== null)}
			>
				<Select
					value={task.notebookId === null ? ANY_NOTEBOOK : String(task.notebookId)}
					onValueChange={(v) => onChange({ notebookId: v === ANY_NOTEBOOK ? null : Number(v) })}
				>
					<SelectTrigger id={notebookId} aria-label={ANSWER_FROM.label} className="w-full">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value={ANY_NOTEBOOK}>{ANSWER_FROM.any}</SelectItem>
						{(notebooks ?? []).map((notebook) => (
							<SelectItem key={notebook.id} value={String(notebook.id)}>
								{notebook.title}
							</SelectItem>
						))}
						{pickedMissing ? (
							<SelectItem value={String(task.notebookId)}>{DELETED_NOTEBOOK}</SelectItem>
						) : null}
					</SelectContent>
				</Select>
			</Field>
			<Field
				label={SAVE_RESULT_TO.label}
				htmlFor={saveToNotebookId}
				hint={SAVE_RESULT_TO.hint(task.saveToNotebookId !== null)}
			>
				<Select
					value={task.saveToNotebookId === null ? DONT_SAVE : String(task.saveToNotebookId)}
					onValueChange={(v) => onChange({ saveToNotebookId: v === DONT_SAVE ? null : Number(v) })}
				>
					<SelectTrigger id={saveToNotebookId} aria-label={SAVE_RESULT_TO.label} className="w-full">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value={DONT_SAVE}>{SAVE_RESULT_TO.none}</SelectItem>
						{(notebooks ?? []).map((notebook) => (
							<SelectItem key={notebook.id} value={String(notebook.id)}>
								{notebook.title}
							</SelectItem>
						))}
						{saveTargetMissing ? (
							<SelectItem value={String(task.saveToNotebookId)}>{DELETED_NOTEBOOK}</SelectItem>
						) : null}
					</SelectContent>
				</Select>
			</Field>
		</div>
	);
}
