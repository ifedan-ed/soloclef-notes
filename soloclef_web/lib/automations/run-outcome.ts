import type { RunStatus } from "@/contracts/types/automation.types";

/**
 * How a run (or one of its steps) ended, in words for the run page. Mirrors
 * the ``error.type`` values the backend writes:
 * - ``app/automations/runtime/time_limit.py`` (``TimedOut``)
 * - ``app/automations/runtime/step.py`` (``StepTimedOut``)
 * - ``app/automations/runtime/cancellation.py`` (``Cancelled``)
 */
export type RunErrorTone = "destructive" | "warning" | "muted";

const RUN_ERRORS: Record<string, { title: string; tone: RunErrorTone }> = {
	NeedsAttention: { title: "Needs your attention", tone: "warning" },
	AgentReportedFailure: { title: "The agent couldn't do this task", tone: "destructive" },
	AutomationBillingDeniedError: {
		title: "Not charged: billing refused the step",
		tone: "destructive",
	},
	TimedOut: { title: "Hit its time limit", tone: "warning" },
	StepTimedOut: { title: "A task ran out of time", tone: "warning" },
	Cancelled: { title: "Cancelled", tone: "muted" },
};

/** Title and tone for a run or step ``error.type``; unknown types show as-is. */
export function describeRunError(
	type: string | undefined,
	fallback = "Run failed"
): { title: string; tone: RunErrorTone } {
	if (type && type in RUN_ERRORS) return RUN_ERRORS[type];
	return { title: type ?? fallback, tone: "destructive" };
}

/** A run that hasn't finished yet: waiting to start or running. Only these can be cancelled. */
export function isRunInFlight(status: RunStatus): boolean {
	return status === "pending" || status === "running";
}
