import type { Execution } from "@/contracts/types/automation.types";

/**
 * The "If already running" choices (``execution.concurrency``), as the form
 * and the automation page name them. What each one does is enforced in
 * ``surfsense_backend/app/automations/concurrency``.
 */
export const CONCURRENCY_OPTIONS: ReadonlyArray<{
	value: Execution["concurrency"];
	label: string;
}> = [
	{ value: "drop_if_running", label: "Skip if already running" },
	{ value: "queue", label: "Queue the next run" },
	{ value: "always", label: "Always run" },
];

export function concurrencyLabel(value: string): string {
	return CONCURRENCY_OPTIONS.find((option) => option.value === value)?.label ?? value;
}
