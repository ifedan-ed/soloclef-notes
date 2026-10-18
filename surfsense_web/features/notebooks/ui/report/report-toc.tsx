"use client";

import { useTranslations } from "next-intl";
import { cn } from "@/lib/utils";

export interface TocItem {
	index: number;
	label: string;
}

/** The report's numbered contents; the section being read is marked current. */
export function ReportToc({
	items,
	active,
	onJump,
	className,
}: {
	items: readonly TocItem[];
	active: number;
	onJump: (index: number) => void;
	className?: string;
}) {
	const t = useTranslations("report_view");
	return (
		<nav aria-label={t("contents")} className={className}>
			<ol className="space-y-0.5">
				{items.map((item) => {
					const current = item.index === active;
					return (
						<li key={item.index}>
							<button
								type="button"
								onClick={() => onJump(item.index)}
								aria-current={current ? "location" : undefined}
								className={cn(
									"flex w-full min-w-0 gap-2 rounded-lg px-3 py-2 text-left text-sm transition-colors hover:bg-accent",
									current
										? "bg-accent font-medium text-foreground"
										: "text-muted-foreground hover:text-foreground"
								)}
							>
								<span className="shrink-0 tabular-nums">{item.index + 1}.</span>
								<span className="min-w-0 break-words">{item.label}</span>
							</button>
						</li>
					);
				})}
			</ol>
		</nav>
	);
}
