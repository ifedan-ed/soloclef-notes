"use client";

import { AlertCircle, Check, RefreshCw } from "lucide-react";
import { useTranslations } from "next-intl";
import { Spinner } from "@/components/ui/spinner";
import { type RefreshState, refreshLabelKey } from "../lib/refresh-state";

/** Icon and text of the menu item for one refresh state. */
export function RefreshFromNextcloudLabel({ state }: { state: RefreshState }) {
	const t = useTranslations("nextcloud_files");
	const icon =
		state.phase === "pending" ? (
			<Spinner size="xs" className="mr-2" />
		) : state.phase === "updated" || state.phase === "unchanged" ? (
			<Check className="mr-2 h-4 w-4" />
		) : state.phase === "error" ? (
			<AlertCircle className="mr-2 h-4 w-4 text-destructive" />
		) : (
			<RefreshCw className="mr-2 h-4 w-4" />
		);
	return (
		<>
			{icon}
			<span
				className={state.phase === "error" ? "text-destructive" : undefined}
				role={state.phase === "idle" ? undefined : "status"}
			>
				{t(refreshLabelKey(state))}
			</span>
		</>
	);
}
