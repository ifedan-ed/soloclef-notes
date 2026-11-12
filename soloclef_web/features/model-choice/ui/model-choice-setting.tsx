"use client";

import { Label } from "@/components/ui/label";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { optionLabel, ownMark } from "@/features/chat-model/model-option";
import type { DefaultSource, ModelJob, ModelRole } from "../model-choice";
import { defaultLabel, type ModelChoice, unavailableSentence } from "../model-choice";
import {
	useChooseModel,
	useChooseModelRole,
	useModelChoice,
	useModelRoles,
} from "../use-model-choice";

/** Radix reserves the empty string for "no value", so the default needs a token of its own. */
const SERVER_DEFAULT = "__server_default__";

interface ModelChoiceFieldProps {
	label: string;
	description: string;
	choice: ModelChoice | undefined;
	/** Whose model the default is: the server's, or the account's chat model. */
	defaultSource: DefaultSource;
	/** The sentence of a read that failed, when it did. */
	readError: string | null;
	onChoose: (model: string | null) => void;
}

/**
 * One model setting: the same control as the chat model's
 * (features/chat-model/ui/chat-model-setting.tsx), over any job's choice.
 */
function ModelChoiceField({
	label,
	description,
	choice,
	defaultSource,
	readError,
	onChoose,
}: ModelChoiceFieldProps) {
	const warning = choice ? unavailableSentence(choice) : null;
	return (
		<div className="flex flex-col gap-2">
			<Label>{label}</Label>
			<p className="text-xs text-muted-foreground">{description}</p>
			<Select
				value={choice ? (choice.selected ?? SERVER_DEFAULT) : ""}
				onValueChange={(value) => onChoose(value === SERVER_DEFAULT ? null : value)}
				disabled={!choice}
			>
				<SelectTrigger className="w-full [&>span]:truncate" aria-label={label}>
					<SelectValue placeholder={readError ? "Unavailable" : "Loading"}>
						{/* Given outright: a chosen model that has left the list has
						    no item to take its label from. */}
						{choice
							? choice.selected !== null
								? optionLabel(choice.models, choice.selected)
								: defaultLabel(choice, defaultSource)
							: null}
					</SelectValue>
				</SelectTrigger>
				{choice ? (
					// No wider than the control: a proxy name can be longer than a
					// phone is wide, and the list would otherwise run off the screen.
					<SelectContent className="max-w-[var(--radix-select-trigger-width)]">
						<SelectItem value={SERVER_DEFAULT}>{defaultLabel(choice, defaultSource)}</SelectItem>
						{choice.models.map((option) => (
							<SelectItem
								key={option.id}
								value={option.id}
								className="break-all"
								// An own model is said to be the account's, under its name.
								description={ownMark(option) ?? undefined}
							>
								{option.label}
							</SelectItem>
						))}
					</SelectContent>
				) : null}
			</Select>
			{warning ? (
				<p role="alert" className="text-xs text-amber-700 dark:text-amber-400">
					{warning}
				</p>
			) : readError ? (
				<p role="alert" className="text-xs text-destructive">
					{readError}
				</p>
			) : null}
		</div>
	);
}

const READ_FAILED = "The models could not be read.";

/** The quick-tasks or the Studio model on the model settings page. */
export function ModelJobSetting({
	job,
	label,
	description,
}: {
	job: ModelJob;
	label: string;
	description: string;
}) {
	const choice = useModelChoice(job);
	const choose = useChooseModel(job);
	return (
		<ModelChoiceField
			label={label}
			description={description}
			choice={choice.data}
			defaultSource="chat"
			readError={choice.isError ? choice.error.message || READ_FAILED : null}
			onChoose={(model) => choose.mutate(model)}
		/>
	);
}

/** The vision or the image model on the model settings page. */
export function ModelRoleSetting({
	kind,
	label,
	description,
}: {
	// Not `role`: on a component that reads as the ARIA attribute.
	kind: ModelRole;
	label: string;
	description: string;
}) {
	const roles = useModelRoles();
	const choose = useChooseModelRole(kind);
	return (
		<ModelChoiceField
			label={label}
			description={description}
			choice={roles.data?.[kind]}
			defaultSource="server"
			readError={roles.isError ? roles.error.message || READ_FAILED : null}
			onChoose={(model) => choose.mutate(model)}
		/>
	);
}
