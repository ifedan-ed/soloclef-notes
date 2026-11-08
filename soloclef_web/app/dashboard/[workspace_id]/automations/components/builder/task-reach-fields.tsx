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
import { Field } from "./form-field";

const SCOPES: { value: TaskScope; label: string; hint: string }[] = [
	{
		value: "documents",
		label: "Your documents",
		hint: "Reads the workspace's documents only.",
	},
	{
		value: "all",
		label: "Your documents and the web",
		hint: "Can also search the web and read pages (Tavily, Exa, Browserbase).",
	},
	{
		value: "web",
		label: "The web only",
		hint: "Searches the web and connected apps, not your documents.",
	},
];

const ANY_NOTEBOOK = "any";
const DONT_SAVE = "none";

interface TaskReachFieldsProps {
	task: BuilderTask;
	workspaceId: number;
	onChange: (patch: Partial<BuilderTask>) => void;
}

/** What one automation task may read: the web or not, and which notebook. */
export function TaskReachFields({ task, workspaceId, onChange }: TaskReachFieldsProps) {
	const scopeId = useId();
	const notebookId = useId();
	const saveToNotebookId = useId();
	const { data: notebooks } = useNotebooks(workspaceId);
	const scope = SCOPES.find((option) => option.value === task.scope) ?? SCOPES[0];
	// A notebook that was deleted still shows as picked until changed.
	const pickedMissing =
		task.notebookId !== null && !(notebooks ?? []).some((n) => n.id === task.notebookId);
	const saveTargetMissing =
		task.saveToNotebookId !== null &&
		!(notebooks ?? []).some((n) => n.id === task.saveToNotebookId);

	return (
		<div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
			<Field label="Can use" htmlFor={scopeId} hint={scope.hint}>
				<Select value={task.scope} onValueChange={(v) => onChange({ scope: v as TaskScope })}>
					<SelectTrigger id={scopeId} aria-label="Can use" className="w-full">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						{SCOPES.map((option) => (
							<SelectItem key={option.value} value={option.value}>
								{option.label}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
			</Field>
			<Field
				label="Notebook"
				htmlFor={notebookId}
				hint={
					task.notebookId === null
						? "Any notebook: the agent can list and read them all."
						: "Answers only from this notebook's switched-on sources."
				}
			>
				<Select
					value={task.notebookId === null ? ANY_NOTEBOOK : String(task.notebookId)}
					onValueChange={(v) => onChange({ notebookId: v === ANY_NOTEBOOK ? null : Number(v) })}
				>
					<SelectTrigger id={notebookId} aria-label="Notebook" className="w-full">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value={ANY_NOTEBOOK}>Any notebook</SelectItem>
						{(notebooks ?? []).map((notebook) => (
							<SelectItem key={notebook.id} value={String(notebook.id)}>
								{notebook.title}
							</SelectItem>
						))}
						{pickedMissing ? (
							<SelectItem value={String(task.notebookId)}>A deleted notebook</SelectItem>
						) : null}
					</SelectContent>
				</Select>
			</Field>
			<Field
				label="Save results to notebook"
				htmlFor={saveToNotebookId}
				hint={
					task.saveToNotebookId === null
						? "Not saved anywhere automatically."
						: "The final result is saved there as a note each run."
				}
			>
				<Select
					value={task.saveToNotebookId === null ? DONT_SAVE : String(task.saveToNotebookId)}
					onValueChange={(v) =>
						onChange({ saveToNotebookId: v === DONT_SAVE ? null : Number(v) })
					}
				>
					<SelectTrigger id={saveToNotebookId} aria-label="Save results to notebook" className="w-full">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value={DONT_SAVE}>Don't save</SelectItem>
						{(notebooks ?? []).map((notebook) => (
							<SelectItem key={notebook.id} value={String(notebook.id)}>
								{notebook.title}
							</SelectItem>
						))}
						{saveTargetMissing ? (
							<SelectItem value={String(task.saveToNotebookId)}>A deleted notebook</SelectItem>
						) : null}
					</SelectContent>
				</Select>
			</Field>
		</div>
	);
}
