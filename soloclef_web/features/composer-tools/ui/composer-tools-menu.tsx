"use client";

import { Check, Wrench } from "lucide-react";
import { useTranslations } from "next-intl";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import {
	COMPOSER_TOOL_ICONS,
	type ComposerToolId,
	type KeyedToolId,
	offeredComposerTools,
} from "../composer-tools";

const NO_KEYED_TOOLS: ReadonlySet<KeyedToolId> = new Set();

type ComposerToolsMenuItemsProps = {
	selected: ComposerToolId | null;
	onSelect: (tool: ComposerToolId) => void;
	/** Exa, Tavily and Browserbase are listed only when the workspace has their key. */
	connected?: ReadonlySet<KeyedToolId>;
};

/** The tools a message can be forced to use; must sit inside a DropdownMenu. */
export function ComposerToolsMenuItems({
	selected,
	onSelect,
	connected = NO_KEYED_TOOLS,
}: ComposerToolsMenuItemsProps) {
	const t = useTranslations("composer_tools");
	const names = useTranslations("scraper_tools");
	return (
		<>
			<DropdownMenuLabel className="text-xs font-normal text-muted-foreground">
				{t("menu_label")}
			</DropdownMenuLabel>
			<DropdownMenuSeparator />
			{offeredComposerTools(connected).map((id) => {
				const Icon = COMPOSER_TOOL_ICONS[id];
				return (
					<DropdownMenuItem key={id} onSelect={() => onSelect(id)} className="gap-2">
						<Icon className="size-4" />
						<span className="min-w-0 flex-1 truncate">{names(id)}</span>
						{selected === id ? <Check className="size-4" aria-hidden /> : null}
					</DropdownMenuItem>
				);
			})}
		</>
	);
}

type ComposerToolsMenuProps = ComposerToolsMenuItemsProps & {
	/** Icon-only on narrow composers, where the label does not fit. */
	compact?: boolean;
};

/** Composer pill that picks one tool for the next message. */
export function ComposerToolsMenu({
	selected,
	onSelect,
	connected,
	compact = false,
}: ComposerToolsMenuProps) {
	const t = useTranslations("composer_tools");
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<button
					type="button"
					aria-label={t("button_aria")}
					className={cn(
						"inline-flex h-9 shrink-0 items-center justify-center gap-1.5 rounded-full border border-primary/10 bg-secondary/50 text-xs font-medium text-muted-foreground outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 data-[state=open]:text-foreground",
						compact ? "w-9" : "px-3"
					)}
				>
					<Wrench className="size-3.5" aria-hidden />
					{compact ? null : t("button")}
				</button>
			</DropdownMenuTrigger>
			<DropdownMenuContent
				className="w-56"
				side="bottom"
				align="start"
				sideOffset={8}
				onCloseAutoFocus={(event) => event.preventDefault()}
			>
				<ComposerToolsMenuItems selected={selected} onSelect={onSelect} connected={connected} />
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
