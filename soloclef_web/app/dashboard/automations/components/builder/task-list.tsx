"use client";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { type BuilderTask, emptyTask } from "@/lib/automations/builder-schema";
import { TaskItem } from "./task-item";

interface TaskListProps {
	tasks: BuilderTask[];
	/** The automation's own model, which a task without one runs on. */
	automationModel: string | null;
	errors: Record<string, string>;
	onChange: (tasks: BuilderTask[]) => void;
}

/**
 * Ordered list of agent tasks. Steps run sequentially in the order shown.
 * Reordering is done with up/down buttons to avoid a drag-and-drop dependency.
 */
export function TaskList({ tasks, automationModel, errors, onChange }: TaskListProps) {
	function updateAt(index: number, patch: Partial<BuilderTask>) {
		onChange(tasks.map((task, i) => (i === index ? { ...task, ...patch } : task)));
	}

	function removeAt(index: number) {
		onChange(tasks.filter((_, i) => i !== index));
	}

	function move(index: number, direction: -1 | 1) {
		const target = index + direction;
		if (target < 0 || target >= tasks.length) return;
		const next = [...tasks];
		[next[index], next[target]] = [next[target], next[index]];
		onChange(next);
	}

	return (
		<div className="space-y-3">
			{tasks.map((task, index) => (
				<TaskItem
					key={task.id}
					index={index}
					total={tasks.length}
					task={task}
					automationModel={automationModel}
					error={errors[`tasks.${index}.query`]}
					onChange={(patch) => updateAt(index, patch)}
					onMoveUp={() => move(index, -1)}
					onMoveDown={() => move(index, 1)}
					onRemove={() => removeAt(index)}
				/>
			))}

			{errors.tasks && <p className="text-xs text-destructive">{errors.tasks}</p>}

			<Button type="button" size="sm" onClick={() => onChange([...tasks, emptyTask()])}>
				<Plus className="h-4 w-4" />
				Add task
			</Button>
		</div>
	);
}
