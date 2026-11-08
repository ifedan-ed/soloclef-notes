"use client";

import { RotateCw } from "lucide-react";
import { cn } from "@/lib/utils";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";

/**
 * Shows the next ideas. While a fresh pool is on its way the current chips
 * stay painted; the icon only starts turning if the wait passes 250 ms.
 */
export function MoreIdeasButton({
	onClick,
	busy,
	label,
}: {
	onClick: () => void;
	busy: boolean;
	label: string;
}) {
	const spinning = useDelayedFlag(busy);
	return (
		<button
			type="button"
			onClick={onClick}
			aria-busy={busy}
			className="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
		>
			<RotateCw className={cn("size-3", spinning && "animate-spin")} aria-hidden />
			{label}
		</button>
	);
}
