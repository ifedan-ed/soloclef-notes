"use client";

import { X } from "lucide-react";
import { useTranslations } from "next-intl";
import { COMPOSER_TOOL_ICONS, type ComposerToolId } from "../composer-tools";

type PreferredToolChipProps = {
	tool: ComposerToolId | null;
	onRemove: () => void;
};

/** Shows which tool the next message is forced to use, with a way to drop it. */
export function PreferredToolChip({ tool, onRemove }: PreferredToolChipProps) {
	const t = useTranslations("composer_tools");
	const names = useTranslations("scraper_tools");
	if (!tool) return null;
	const Icon = COMPOSER_TOOL_ICONS[tool];
	const name = names(tool);
	return (
		<div className="mx-3 mt-2 flex">
			<span
				title={t("chip_hint", { tool: name })}
				className="inline-flex h-7 max-w-full items-center gap-1.5 rounded-full border border-primary/10 bg-secondary/50 pr-1 pl-2.5 text-xs font-medium text-foreground"
			>
				<Icon className="size-3.5" />
				<span className="truncate">{name}</span>
				<button
					type="button"
					onClick={onRemove}
					aria-label={t("remove", { tool: name })}
					className="inline-flex size-5 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-foreground/10 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 outline-none"
				>
					<X className="size-3" aria-hidden />
				</button>
			</span>
		</div>
	);
}
