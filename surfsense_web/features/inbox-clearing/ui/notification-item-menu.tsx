"use client";

import { MoreHorizontal, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

interface NotificationItemMenuProps {
	title: string;
	onDelete: () => void;
	className?: string;
}

/**
 * A notification's own menu: Delete. With a mouse or trackpad it shows on
 * hover or focus; on a touch screen (a phone, an iPad, at any width) always,
 * as there is no hover to bring it up.
 */
export function NotificationItemMenu({ title, onDelete, className }: NotificationItemMenuProps) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button
					type="button"
					variant="ghost"
					size="icon"
					aria-label={`Actions for ${title}`}
					className={cn(
						"h-7 w-7 shrink-0 text-muted-foreground transition-opacity",
						"pointer-fine:opacity-0 pointer-fine:group-hover:opacity-100 pointer-fine:focus-visible:opacity-100 pointer-fine:data-[state=open]:opacity-100",
						className
					)}
				>
					<MoreHorizontal className="h-3.5 w-3.5" />
				</Button>
			</DropdownMenuTrigger>
			{/* Above the Notifications panel (z-80). */}
			<DropdownMenuContent align="end" className="z-90 w-32">
				<DropdownMenuItem onSelect={onDelete}>
					<Trash2 className="mr-2 h-4 w-4" />
					Delete
				</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
