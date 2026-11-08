"use client";

import type { FC } from "react";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import { ENABLED_IN_WORKSPACE_LABEL } from "./enabled-in-workspace";

interface EnabledInWorkspaceSwitchProps {
	checked: boolean;
	disabled?: boolean;
	onCheckedChange: (enabled: boolean) => void;
	className?: string;
}

/** A connection's on/off switch for the current workspace, with its label. */
export const EnabledInWorkspaceSwitch: FC<EnabledInWorkspaceSwitchProps> = ({
	checked,
	disabled,
	onCheckedChange,
	className,
}) => (
	<div className={cn("flex items-center gap-2", className)}>
		<span className="text-xs sm:text-sm text-muted-foreground">{ENABLED_IN_WORKSPACE_LABEL}</span>
		<Switch
			aria-label={ENABLED_IN_WORKSPACE_LABEL}
			checked={checked}
			disabled={disabled}
			onCheckedChange={onCheckedChange}
			className="shrink-0"
		/>
	</div>
);
