"use client";

import type { FC } from "react";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";

/**
 * The one name for a connection's on/off switch, used by the switch itself
 * and by every place that shows its state.
 */
export const ENABLED_LABEL = "Enabled";

interface EnabledSwitchProps {
	checked: boolean;
	disabled?: boolean;
	onCheckedChange: (enabled: boolean) => void;
	className?: string;
}

/**
 * A connection's own on/off switch, with its label. It shows the
 * connection's `enabled` field and saves with PUT .../{id}/enabled.
 */
export const EnabledSwitch: FC<EnabledSwitchProps> = ({
	checked,
	disabled,
	onCheckedChange,
	className,
}) => (
	<div className={cn("flex items-center gap-2", className)}>
		<span className="text-xs sm:text-sm text-muted-foreground">{ENABLED_LABEL}</span>
		<Switch
			aria-label={ENABLED_LABEL}
			checked={checked}
			disabled={disabled}
			onCheckedChange={onCheckedChange}
			className="shrink-0"
		/>
	</div>
);
