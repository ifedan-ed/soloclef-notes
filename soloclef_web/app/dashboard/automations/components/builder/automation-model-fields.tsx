"use client";

import { Check, ChevronsUpDown, Search, UserRound } from "lucide-react";
import type { ReactNode } from "react";
import { useId, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Skeleton } from "@/components/ui/skeleton";
import { matchingModels } from "@/features/chat-model/chat-model";
import { ModelFamilyIcon } from "@/features/chat-model/model-family-icon";
import { optionLabel } from "@/features/chat-model/model-option";
import { ModelOptionText } from "@/features/chat-model/ui/model-option-text";
import { useChatModel } from "@/features/chat-model/use-chat-model";
import { accountModelLine, withdrawnSentence } from "@/lib/automations/automation-model";
import { cn } from "@/lib/utils";
import { Field } from "./form-field";

interface AutomationModelFieldProps {
	label: string;
	/** A model's id (a proxy name, or "own:<n>"), or null for "none chosen here". */
	value: string | null;
	onChange: (name: string | null) => void;
	/** What "none chosen" is called, and the line under it. */
	unsetLabel: string;
	unsetDetail?: string;
	hint?: string;
}

const ROW =
	"flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:bg-accent focus-visible:outline-none";

/**
 * One model picker for the automation builder: the models the proxy offers
 * (the chat model menu's own list), plus a first row for choosing none.
 *
 * The value is the model's id, which is what the definition stores and what
 * a run resolves: a proxy name, or "own:<n>" for a model of the account's own
 * connection, shown by its label. A model no longer listed stays selected
 * and is said under the field.
 */
export function AutomationModelField({
	label,
	value,
	onChange,
	unsetLabel,
	unsetDetail,
	hint,
}: AutomationModelFieldProps) {
	const triggerId = useId();
	const [open, setOpen] = useState(false);
	const [search, setSearch] = useState("");
	const chatModel = useChatModel();
	const data = chatModel.data;
	const options = useMemo(() => (data ? matchingModels(data, search) : []), [data, search]);
	// The trigger shows a label: an own model's id is a number, not a name.
	const valueLabel = value === null ? null : optionLabel(data?.models ?? [], value);
	const searching = search.trim().length > 0;

	if (chatModel.isLoading) {
		return (
			<Field label={label}>
				<Skeleton className="h-9 w-full" />
			</Field>
		);
	}

	function pick(name: string | null) {
		onChange(name);
		setSearch("");
		setOpen(false);
	}

	// The list could not be read: a choice already made is kept and shown,
	// and the field says why nothing else can be picked.
	const unreadable = chatModel.isError
		? chatModel.error.message || "The models could not be read. Try again in a moment."
		: null;

	return (
		<Field
			label={label}
			htmlFor={triggerId}
			hint={hint}
			error={unreadable ?? withdrawnSentence(data, value) ?? undefined}
		>
			<Popover
				open={open}
				onOpenChange={(next) => {
					if (!next) setSearch("");
					// The list is the proxy's, read live: opening is when a model
					// added or removed there should show.
					if (next) void chatModel.refetch({ cancelRefetch: false });
					setOpen(next);
				}}
			>
				<PopoverTrigger asChild>
					<Button
						id={triggerId}
						type="button"
						variant="outline"
						role="combobox"
						aria-expanded={open}
						aria-label={label}
						className="h-9 w-full justify-between gap-2 bg-transparent px-3 font-normal"
					>
						<span className="flex min-w-0 items-center gap-2">
							{value === null ? (
								<UserRound className="size-4 shrink-0 text-muted-foreground" aria-hidden />
							) : (
								<ModelFamilyIcon name={valueLabel ?? value} className="size-4 shrink-0" />
							)}
							<span className="truncate">{valueLabel ?? unsetLabel}</span>
						</span>
						<ChevronsUpDown className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
					</Button>
				</PopoverTrigger>
				<PopoverContent
					align="start"
					className="w-(--radix-popover-trigger-width) min-w-72 max-w-[calc(100vw-2rem)] border border-popover-border bg-popover p-0 text-popover-foreground shadow-md"
				>
					<div className="flex max-h-[min(360px,60vh)] min-h-0 flex-col overflow-hidden">
						<div className="border-b border-border/60 p-2">
							<div className="relative">
								<Search
									className="absolute left-2 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
									aria-hidden
								/>
								<Input
									value={search}
									onChange={(event) => setSearch(event.target.value)}
									placeholder="Search models"
									aria-label="Search models"
									className="h-8 border-0 bg-transparent pl-8 text-sm shadow-none focus-visible:ring-0"
								/>
							</div>
						</div>
						<div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden p-1.5">
							{/* Choosing none is a setting, not a model with a name to search for. */}
							{searching ? null : (
								<Row selected={value === null} onPick={() => pick(null)}>
									<UserRound className="size-4 shrink-0 text-muted-foreground" aria-hidden />
									<span className="min-w-0 flex-1">
										<span className="block">{unsetLabel}</span>
										{unsetDetail ? (
											<span className="block truncate text-xs text-muted-foreground">
												{unsetDetail}
											</span>
										) : null}
									</span>
								</Row>
							)}
							{options.length === 0 ? (
								<p className="px-3 py-6 text-center text-sm text-muted-foreground">
									{searching
										? "No model matches that search."
										: (unreadable ?? "The server offers no chat models.")}
								</p>
							) : (
								options.map((option) => (
									<Row
										key={option.id}
										selected={value === option.id}
										onPick={() => pick(option.id)}
									>
										<ModelFamilyIcon name={option.label} className="size-4 shrink-0" />
										<ModelOptionText option={option} />
									</Row>
								))
							)}
						</div>
					</div>
				</PopoverContent>
			</Popover>
		</Field>
	);
}

function Row({
	selected,
	onPick,
	children,
}: {
	selected: boolean;
	onPick: () => void;
	children: ReactNode;
}) {
	return (
		<button type="button" className={cn(ROW)} onClick={onPick}>
			{children}
			{selected ? <Check className="size-4 shrink-0" aria-label="Selected" /> : null}
		</button>
	);
}

/**
 * The automation-wide model: every task is answered by it unless the task
 * names its own. Choosing none follows the account's chat model, which is
 * named here as it stands today.
 */
export function AutomationChatModelField({
	value,
	onChange,
}: {
	value: string | null;
	onChange: (name: string | null) => void;
}) {
	const { data } = useChatModel();
	return (
		<AutomationModelField
			label="Runs on"
			value={value}
			onChange={onChange}
			unsetLabel="Your chat model"
			unsetDetail={accountModelLine(data)}
			hint={
				value === null
					? `Runs follow the model your chat uses. ${accountModelLine(data)}.`
					: "Every task runs on this model unless the task picks its own."
			}
		/>
	);
}
