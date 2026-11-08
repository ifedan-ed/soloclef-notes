"use client";

import { useTranslations } from "next-intl";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { STUDIO_TILES } from "../../lib/studio-catalog";
import type { StudioKind } from "../../model/notebook";

export function StudioTiles({ onPick }: { onPick: (kind: StudioKind) => void }) {
	const t = useTranslations("notebooks");
	return (
		<div className="grid grid-cols-2 gap-2">
			{STUDIO_TILES.map((tile) => {
				const Icon = tile.icon;
				return (
					<Tooltip key={tile.kind}>
						<TooltipTrigger asChild>
							<button
								type="button"
								onClick={() => onPick(tile.kind)}
								className="flex min-h-12 items-center gap-2 rounded-xl border bg-muted/30 px-3 py-2 text-left text-sm font-medium transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
							>
								<Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden />
								<span className="min-w-0 truncate">{t(`kinds.${tile.kind}.label`)}</span>
							</button>
						</TooltipTrigger>
						<TooltipContent side="bottom" className="max-w-56">
							{t(`kinds.${tile.kind}.tooltip`)}
						</TooltipContent>
					</Tooltip>
				);
			})}
		</div>
	);
}
