"use client";

import { ChevronDown, Telescope, Zap } from "lucide-react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { type ResearchMode, researchModes } from "../../../model/research";

const MODE_ICONS = { fast: Zap, deep: Telescope } as const;

function isResearchMode(value: string): value is ResearchMode {
	return (researchModes as readonly string[]).includes(value);
}

/** Fast research skims for a few sources; deep research reads widely first. */
export function ResearchModeMenu({
	value,
	onChange,
}: {
	value: ResearchMode;
	onChange: (value: ResearchMode) => void;
}) {
	const t = useTranslations("notebook_research");
	const Icon = MODE_ICONS[value];
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button
					type="button"
					variant="outline"
					size="sm"
					className="h-7 min-w-0 shrink gap-1.5 rounded-full px-2.5 text-xs"
					aria-label={t("mode_label", { mode: t(`mode_${value}`) })}
				>
					<Icon className="size-3.5 shrink-0" />
					<span className="truncate">{t(`mode_${value}`)}</span>
					<ChevronDown className="size-3 shrink-0 opacity-60" />
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className="w-64">
				<DropdownMenuRadioGroup
					value={value}
					onValueChange={(next) => {
						if (isResearchMode(next)) onChange(next);
					}}
				>
					{researchModes.map((mode) => {
						const ModeIcon = MODE_ICONS[mode];
						return (
							<DropdownMenuRadioItem key={mode} value={mode} className="items-start">
								<ModeIcon className="mt-0.5 size-4" />
								<span className="flex min-w-0 flex-col">
									<span className="font-medium">{t(`mode_${mode}`)}</span>
									<span className="text-xs text-muted-foreground">
										{t(`mode_${mode}_description`)}
									</span>
								</span>
							</DropdownMenuRadioItem>
						);
					})}
				</DropdownMenuRadioGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
