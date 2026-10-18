"use client";

import { TriangleAlert } from "lucide-react";
import { memo, useId } from "react";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import {
	Select,
	SelectContent,
	SelectGroup,
	SelectItem,
	SelectLabel,
	SelectSeparator,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import {
	type EligibleModelKind,
	type EligibleModelOption,
	useAutomationEligibleModels,
} from "@/hooks/use-automation-eligible-models";
import { getProviderIcon } from "@/lib/provider-icons";
import { Field } from "./form-field";

export interface AutomationModelSelection {
	/** ``0`` is Auto; ``null`` means not chosen yet. */
	chatModelId: number | null;
	imageConfigId: number;
	visionConfigId: number;
}

interface AutomationModelFieldsProps {
	/** Resolved (effective) ids; chat may be Auto (`0`). */
	value: AutomationModelSelection;
	onChange: (patch: Partial<AutomationModelSelection>) => void;
	/** Kept for callers; the fields no longer link to workspace settings. */
	workspaceId: number;
	errors?: Partial<Record<keyof AutomationModelSelection, string>>;
}

/**
 * Three model pickers (Chat / Image / Vision) for the automation builder +
 * chat approval card. Options come from {@link useAutomationEligibleModels}
 * (the shared models, then the user's own); the selection is snapshotted onto
 * `definition.models` at create time.
 */
export function AutomationModelFields({ value, onChange, errors }: AutomationModelFieldsProps) {
	const { llm, image, vision, isLoading } = useAutomationEligibleModels();

	return (
		<div className="flex flex-col gap-4">
			<ModelSelectField
				label="Chat model"
				kind={llm}
				value={value.chatModelId}
				isLoading={isLoading}
				error={errors?.chatModelId}
				onChange={(id) => onChange({ chatModelId: id })}
			/>
			<ModelSelectField
				label="Image model"
				kind={image}
				value={value.imageConfigId}
				isLoading={isLoading}
				error={errors?.imageConfigId}
				onChange={(id) => onChange({ imageConfigId: id })}
			/>
			<ModelSelectField
				label="Vision model"
				kind={vision}
				value={value.visionConfigId}
				isLoading={isLoading}
				error={errors?.visionConfigId}
				onChange={(id) => onChange({ visionConfigId: id })}
			/>
		</div>
	);
}

interface ModelSelectFieldProps {
	label: string;
	kind: EligibleModelKind;
	value: number | null;
	isLoading: boolean;
	error?: string;
	onChange: (id: number) => void;
}

const ModelSelectField = memo(function ModelSelectField({
	label,
	kind,
	value,
	isLoading,
	error,
	onChange,
}: ModelSelectFieldProps) {
	const triggerId = useId();

	if (isLoading) {
		return (
			<Field label={label}>
				<Skeleton className="h-9 w-full" />
			</Field>
		);
	}

	if (kind.options.length === 0) {
		return (
			<Field label={label}>
				<Alert variant="warning">
					<TriangleAlert aria-hidden />
					<AlertTitle>No models available</AlertTitle>
					<AlertDescription className="block leading-5">
						There is no model for this yet. Automations need one for each slot.
					</AlertDescription>
				</Alert>
			</Field>
		);
	}

	const auto = kind.options.find((o) => o.isAuto);
	const shared = kind.options.filter((o) => !o.isBYOK && !o.isAuto);
	const byok = kind.options.filter((o) => o.isBYOK);
	const selected = value != null ? kind.byId.get(value) : undefined;

	return (
		<Field label={label} htmlFor={triggerId} error={error}>
			<Select
				value={selected ? String(value) : undefined}
				onValueChange={(v) => onChange(Number(v))}
			>
				<SelectTrigger
					id={triggerId}
					aria-label={label}
					aria-invalid={error ? true : undefined}
					className="w-full"
				>
					{selected ? (
						<span className="flex items-center gap-2">
							{selected.isAuto ? null : getProviderIcon(selected.provider)}
							<span className="truncate">{selected.name}</span>
						</span>
					) : (
						<SelectValue placeholder="Select a model" />
					)}
				</SelectTrigger>
				<SelectContent matchTriggerWidth={false} className="w-auto min-w-80 max-w-[90vw]">
					{auto ? (
						<>
							<SelectItem value={String(auto.id)}>
								<span className="flex flex-col">
									<span>{auto.name}</span>
									<span className="text-xs text-muted-foreground">
										Picks a standard model for each run
									</span>
								</span>
							</SelectItem>
							<SelectSeparator />
						</>
					) : null}
					{shared.length > 0 ? (
						<SelectGroup>
							<SelectLabel>Available models</SelectLabel>
							{shared.map((option) => (
								<ModelOption key={option.id} option={option} />
							))}
						</SelectGroup>
					) : null}
					{shared.length > 0 && byok.length > 0 ? <SelectSeparator /> : null}
					{byok.length > 0 ? (
						<SelectGroup>
							<SelectLabel>Your models</SelectLabel>
							{byok.map((option) => (
								<ModelOption key={option.id} option={option} badge="BYOK" />
							))}
						</SelectGroup>
					) : null}
				</SelectContent>
			</Select>
		</Field>
	);
});

function ModelOption({ option, badge }: { option: EligibleModelOption; badge?: "BYOK" }) {
	return (
		<SelectItem value={String(option.id)}>
			<span className="flex items-center gap-2">
				{getProviderIcon(option.provider)}
				<span className="truncate">{option.name}</span>
				{option.premium ? <Badge variant="secondary">Premium</Badge> : null}
				{badge ? <Badge variant="outline">{badge}</Badge> : null}
			</span>
		</SelectItem>
	);
}
