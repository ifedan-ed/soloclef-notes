"use client";

import { PanelLeftClose, PanelLeftOpen, PanelRightClose, PanelRightOpen } from "lucide-react";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/** A collapsible notebook column; collapsed it shrinks to a slim rail. */
export function SidePanel({
	side,
	title,
	collapsed,
	onCollapsedChange,
	collapseLabel,
	expandLabel,
	mobileHidden,
	wide = false,
	bare = false,
	children,
}: {
	side: "left" | "right";
	title: string;
	collapsed: boolean;
	onCollapsedChange: (collapsed: boolean) => void;
	collapseLabel: string;
	expandLabel: string;
	/** Below the desktop breakpoint only the active tab's column shows. */
	mobileHidden: boolean;
	/** Widened for an open Studio output, the chat still usable beside it. */
	wide?: boolean;
	/** No title row: the content brings its own header. */
	bare?: boolean;
	children: ReactNode;
}) {
	const CollapseIcon = side === "left" ? PanelLeftClose : PanelRightClose;
	const ExpandIcon = side === "left" ? PanelLeftOpen : PanelRightOpen;
	return (
		<aside
			className={cn(
				"min-h-0 w-full flex-1 flex-col bg-panel lg:flex-none lg:transition-[width] lg:duration-200 lg:ease-out motion-reduce:lg:transition-none",
				mobileHidden ? "hidden lg:flex" : "flex",
				side === "left" ? "lg:border-r" : "lg:border-l",
				collapsed
					? "lg:w-12"
					: wide
						? "lg:w-[clamp(28rem,46vw,56rem)]"
						: side === "left"
							? "lg:w-80"
							: "lg:w-96"
			)}
			data-wide={wide || undefined}
		>
			<div
				className={cn(
					"flex h-11 shrink-0 items-center gap-2 px-3",
					bare && !collapsed && "hidden",
					collapsed ? "lg:justify-center lg:px-0" : side === "right" && "lg:flex-row-reverse"
				)}
			>
				<Button
					variant="ghost"
					size="icon"
					className="hidden size-8 shrink-0 rounded-full lg:inline-flex"
					onClick={() => onCollapsedChange(!collapsed)}
					aria-label={collapsed ? expandLabel : collapseLabel}
				>
					{collapsed ? <ExpandIcon className="size-4" /> : <CollapseIcon className="size-4" />}
				</Button>
				<h2 className={cn("flex-1 text-sm font-semibold", collapsed && "lg:hidden")}>{title}</h2>
			</div>
			<div className={cn("flex min-h-0 flex-1 flex-col", collapsed && "lg:hidden")}>{children}</div>
		</aside>
	);
}
