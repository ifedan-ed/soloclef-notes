"use client";

import { AlertTriangle, Check, ChevronDown, Search, Server, SlidersHorizontal } from "lucide-react";
import { useRouter } from "next/navigation";
import { useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Drawer,
	DrawerContent,
	DrawerHandle,
	DrawerHeader,
	DrawerTitle,
	DrawerTrigger,
} from "@/components/ui/drawer";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Spinner } from "@/components/ui/spinner";
import { useIsMobile } from "@/hooks/use-mobile";
import { settingsPath } from "@/lib/dashboard-paths";
import { cn } from "@/lib/utils";
import { defaultIsOffered, matchingModels, unavailableSentence } from "../chat-model";
import { ModelFamilyIcon } from "../model-family-icon";
import { optionLabel } from "../model-option";
import { useChatModel, useChooseChatModel } from "../use-chat-model";
import { ModelOptionText } from "./model-option-text";

interface ChatModelPickerProps {
	className?: string;
	/** Called after a choice, so the composer can take the focus back. */
	onChatModelSelected?: () => void;
}

const ROW =
	"flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:bg-accent focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-60 disabled:hover:bg-transparent";

/**
 * The chat header's model menu: which model answers this account, and the
 * models the proxy offers to choose from.
 *
 * A model that has left the proxy is said so in words, here and on the
 * trigger, before a message is sent: the chat would refuse the message with
 * the same sentence, and it is better read while there is still time to
 * choose another.
 */
export function ChatModelPicker({ className, onChatModelSelected }: ChatModelPickerProps) {
	const router = useRouter();
	const isMobile = useIsMobile();
	const [open, setOpen] = useState(false);
	const [search, setSearch] = useState("");
	const chatModel = useChatModel();
	const choose = useChooseChatModel();
	const data = chatModel.data;
	const options = useMemo(() => (data ? matchingModels(data, search) : []), [data, search]);
	// The trigger names the model by its label: an own model's id is a number.
	const modelLabel = data ? optionLabel(data.models, data.model) : null;
	const warning = data ? unavailableSentence(data) : null;
	const searching = search.trim().length > 0;

	function handleOpenChange(nextOpen: boolean) {
		if (!nextOpen) setSearch("");
		// The list is the proxy's, read live: opening the menu is the moment
		// a removed or added model should show (a no-op while still fresh).
		if (nextOpen) void chatModel.refetch({ cancelRefetch: false });
		setOpen(nextOpen);
	}

	function pick(name: string | null) {
		if (data && name !== data.selected) choose.mutate(name);
		setSearch("");
		setOpen(false);
		requestAnimationFrame(() => onChatModelSelected?.());
	}

	function manageModels() {
		setOpen(false);
		router.push(settingsPath("models"));
	}

	const list = !data ? (
		chatModel.isError ? (
			<div className="flex flex-col items-center gap-3 px-4 py-8 text-center" role="alert">
				<p className="text-sm text-muted-foreground">
					{chatModel.error.message || "The chat models could not be read."}
				</p>
				<Button variant="outline" size="sm" onClick={() => void chatModel.refetch()}>
					Try again
				</Button>
			</div>
		) : (
			<div className="flex items-center justify-center py-8">
				<Spinner />
			</div>
		)
	) : (
		<>
			{/* Going back to the default is not offered as a search result: it
			    is a setting, not a model with a name to look for. */}
			{searching ? null : (
				<button
					type="button"
					className={ROW}
					// A default the proxy does not offer cannot answer, so it
					// cannot be gone back to; it stays listed, and says why.
					disabled={!defaultIsOffered(data)}
					onClick={() => pick(null)}
				>
					<Server className="size-4 shrink-0 text-muted-foreground" aria-hidden />
					<span className="min-w-0 flex-1">
						<span className="block font-medium">Server default</span>
						<span className="block truncate text-xs text-muted-foreground" title={data.default}>
							{defaultIsOffered(data) ? data.default : `${data.default} is not offered`}
						</span>
					</span>
					{data.selected === null ? (
						<Check className="size-4 shrink-0" aria-label="Selected" />
					) : null}
				</button>
			)}
			{options.length === 0 ? (
				<p className="px-3 py-8 text-center text-sm text-muted-foreground">
					{searching ? "No model matches that search." : "The server offers no chat models."}
				</p>
			) : (
				<>
					{searching ? null : (
						<div className="px-3 pt-3 pb-1 text-xs font-medium text-muted-foreground">Models</div>
					)}
					{options.map((option) => (
						<button type="button" key={option.id} className={ROW} onClick={() => pick(option.id)}>
							<ModelFamilyIcon name={option.label} className="size-4 shrink-0" />
							<ModelOptionText option={option} />
							{data.selected === option.id ? (
								<Check className="size-4 shrink-0" aria-label="Selected" />
							) : null}
						</button>
					))}
				</>
			)}
		</>
	);

	const content = (
		<div className="flex max-h-[min(420px,70vh)] min-h-0 select-none flex-col overflow-hidden">
			<div className="border-b border-border/60 p-2">
				<div className="relative">
					<Search
						className="absolute left-2 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
						aria-hidden
					/>
					<Input
						value={search}
						onChange={(event) => setSearch(event.target.value)}
						placeholder="Search chat models"
						aria-label="Search chat models"
						className="h-8 border-0 bg-transparent pl-8 text-sm shadow-none focus-visible:ring-0"
					/>
				</div>
			</div>
			{warning ? (
				<p
					role="alert"
					className="mx-2 mt-2 flex gap-2 rounded-md bg-amber-500/10 px-3 py-2 text-xs leading-relaxed text-amber-800 dark:text-amber-300"
				>
					<AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden />
					<span className="min-w-0 break-words">{warning}</span>
				</p>
			) : null}
			<div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden p-1.5">{list}</div>
			{/* Every account may add its own OpenAI-compatible connection, so
			    the way to the model settings is offered to everyone. */}
			<div className="border-t border-border/60 p-2">
				<Button
					variant="ghost"
					className="w-full justify-start rounded-md hover:bg-foreground/10 hover:text-foreground"
					onClick={manageModels}
				>
					<SlidersHorizontal className="size-4" /> Manage models
				</Button>
			</div>
		</div>
	);

	const trigger = (
		<Button
			type="button"
			variant="ghost"
			size="sm"
			data-desktop-layout-overflow-boundary
			aria-label={warning ? `Select chat model. ${warning}` : "Select chat model"}
			title={warning ?? modelLabel ?? undefined}
			className={cn(
				"h-8 min-w-0 select-none gap-2 rounded-md px-3 text-muted-foreground transition-colors",
				"hover:bg-foreground/10 hover:text-foreground",
				"data-[state=open]:bg-foreground/10 data-[state=open]:text-foreground",
				className,
				isMobile && "h-9 w-auto shrink-0 justify-center gap-1 px-2"
			)}
		>
			{warning ? (
				<AlertTriangle className="size-4 shrink-0 text-amber-600 dark:text-amber-400" aria-hidden />
			) : data ? (
				<ModelFamilyIcon name={modelLabel ?? data.model} className="size-4 shrink-0" />
			) : (
				<Server className="size-4 shrink-0" aria-hidden />
			)}
			{/* On a phone the header has room for the icon only; the name is in the sheet. */}
			{isMobile ? null : (
				<span className="min-w-0 max-w-40 flex-1 truncate text-sm">
					{modelLabel ?? "Chat model"}
				</span>
			)}
			<ChevronDown className="size-3.5 shrink-0" aria-hidden />
		</Button>
	);

	if (isMobile) {
		return (
			<Drawer open={open} onOpenChange={handleOpenChange}>
				<DrawerTrigger asChild>{trigger}</DrawerTrigger>
				<DrawerContent className="max-h-[85vh]">
					<DrawerHandle />
					<DrawerHeader>
						<DrawerTitle>Chat model</DrawerTitle>
					</DrawerHeader>
					{content}
				</DrawerContent>
			</Drawer>
		);
	}

	return (
		<Popover open={open} onOpenChange={handleOpenChange}>
			<PopoverTrigger asChild>{trigger}</PopoverTrigger>
			<PopoverContent
				align="start"
				className="w-[340px] border border-popover-border bg-popover p-0 text-popover-foreground shadow-md"
			>
				{content}
			</PopoverContent>
		</Popover>
	);
}
