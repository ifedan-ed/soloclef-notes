import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

interface PagePanelProps {
	children: ReactNode;
	className?: string;
	viewportClassName?: string;
	contentClassName?: string;
}

/**
 * The page area to the right of the rail.
 * Use this when a route should own the whole area instead of rendering
 * inside the normal TabBar/Header/main/right-panel chrome.
 */
export function PagePanel({
	children,
	className,
	viewportClassName,
	contentClassName,
}: PagePanelProps) {
	return (
		<main
			className={cn(
				"relative isolate flex min-w-0 flex-1 flex-col overflow-hidden bg-panel",
				className
			)}
		>
			<div
				className={cn(
					"flex min-h-0 flex-1 items-center justify-center overflow-auto px-4 py-8",
					viewportClassName
				)}
			>
				<div className={cn("w-full max-w-md", contentClassName)}>{children}</div>
			</div>
		</main>
	);
}
