"use client";

import { Label } from "@/components/ui/label";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { defaultIsOffered, unavailableSentence } from "../chat-model";
import { optionLabel, ownMark } from "../model-option";
import { useChatModel, useChooseChatModel } from "../use-chat-model";

/** Radix reserves the empty string for "no value", so the default needs a token of its own. */
const SERVER_DEFAULT = "__server_default__";

/**
 * The chat model on the model settings page: the same choice,
 * read and stored through the same route, as the menu in the chat header.
 */
export function ChatModelSetting() {
	const chatModel = useChatModel();
	const choose = useChooseChatModel();
	const data = chatModel.data;
	const warning = data ? unavailableSentence(data) : null;

	return (
		<div className="flex flex-col gap-2">
			<Label>Chat model</Label>
			<p className="text-xs text-muted-foreground">
				The model that answers in chat. You can also change it from the chat.
			</p>
			<Select
				value={data ? (data.selected ?? SERVER_DEFAULT) : ""}
				onValueChange={(value) => choose.mutate(value === SERVER_DEFAULT ? null : value)}
				disabled={!data}
			>
				<SelectTrigger className="w-full [&>span]:truncate" aria-label="Chat model">
					<SelectValue placeholder={chatModel.isError ? "Unavailable" : "Loading"}>
						{/* Given outright: a chosen model that has left the list has
						    no item to take its label from. */}
						{data
							? data.selected !== null
								? optionLabel(data.models, data.selected)
								: `Server default (${data.default})`
							: null}
					</SelectValue>
				</SelectTrigger>
				{data ? (
					// No wider than the control: a proxy name can be longer than a
					// phone is wide, and the list would otherwise run off the screen.
					<SelectContent className="max-w-[var(--radix-select-trigger-width)]">
						<SelectItem
							value={SERVER_DEFAULT}
							disabled={!defaultIsOffered(data)}
							description={defaultIsOffered(data) ? undefined : "Not offered by the server"}
						>
							Server default ({data.default})
						</SelectItem>
						{data.models.map((option) => (
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
			) : chatModel.isError ? (
				<p role="alert" className="text-xs text-destructive">
					{chatModel.error.message || "The chat models could not be read."}
				</p>
			) : null}
		</div>
	);
}
