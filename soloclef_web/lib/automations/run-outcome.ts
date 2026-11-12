import type { RunStatus } from "@/contracts/types/automation.types";

/**
 * How a run (or one of its steps) ended, in words for the run page. Mirrors
 * the ``error.type`` values a run row holds, the Python tier's and the Go
 * worker's:
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
	// The Go worker's words (soloclef_worker/internal/automationrun).
	approval_refused: { title: "Needs your attention", tone: "warning" },
	needs_attention: { title: "Needs your attention", tone: "warning" },
	timed_out: { title: "Hit its time limit", tone: "warning" },
	cancelled: { title: "Cancelled", tone: "muted" },
	stopped: { title: "Stopped before it finished", tone: "muted" },
	worker_stopped: { title: "Interrupted", tone: "warning" },
	model_unavailable: { title: "The model could not answer", tone: "destructive" },
	step_limit: { title: "Took too many steps", tone: "destructive" },
	question: { title: "Asked a question nobody could answer", tone: "warning" },
	invalid_step: { title: "This task cannot be run as saved", tone: "destructive" },
	invalid_definition: { title: "This automation cannot be run as saved", tone: "destructive" },
	error: { title: "The task failed", tone: "destructive" },
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
