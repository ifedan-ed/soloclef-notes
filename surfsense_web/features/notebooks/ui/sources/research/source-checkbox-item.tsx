"use client";

import * as DropdownMenuPrimitive from "@radix-ui/react-dropdown-menu";
import { CheckIcon } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/**
 * A menu row with a visible checkbox that keeps the menu open when ticked,
 * for picking several research sources in one visit to the menu.
 */
export function SourceCheckboxItem({
	checked,
	disabled,
	onCheckedChange,
	className,
	children,
}: {
	checked: boolean;
	disabled?: boolean;
	onCheckedChange: (checked: boolean) => void;
	className?: string;
	children: ReactNode;
}) {
	return (
		<DropdownMenuPrimitive.CheckboxItem
			checked={checked}
			disabled={disabled}
			onCheckedChange={onCheckedChange}
			// Ticking one source should not close the menu on the next.
			onSelect={(event) => event.preventDefault()}
			className={cn(
				"group relative flex cursor-default items-center gap-2 rounded-sm py-1.5 pr-2 pl-8 text-sm outline-hidden select-none focus:bg-accent focus:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0",
				className
			)}
		>
			<span
				aria-hidden
				className="absolute left-2 grid size-4 place-content-center rounded-[4px] border border-input shadow-xs group-data-[state=checked]:border-primary group-data-[state=checked]:bg-primary group-data-[state=checked]:text-primary-foreground dark:bg-input/30 dark:group-data-[state=checked]:bg-primary"
			>
				<DropdownMenuPrimitive.ItemIndicator>
					<CheckIcon className="size-3.5" />
				</DropdownMenuPrimitive.ItemIndicator>
			</span>
			{children}
		</DropdownMenuPrimitive.CheckboxItem>
	);
}
