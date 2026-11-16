"use client";
import { AlertCircle, AlertTriangle, GitCommitHorizontal, Mail, MailX } from "lucide-react";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import type { RunStatus, RunStepResult } from "@/contracts/types/automation.types";
import { useAutomationRun } from "@/hooks/use-automation-runs";
import { describeRunError } from "@/lib/automations/run-outcome";
import { readRunEmail, runEmailSentence } from "@/lib/automations/run-transcript";
import { cn } from "@/lib/utils";
import { RunStepResultCard } from "./run-step-result-card";

interface RunDetailsPanelProps {
	automationId: number;
	runId: number;
	/** The run's status from the runs list. Used to hide diagnostic sections
	 * that only make sense after the run reaches a terminal state. */
	liveStatus: RunStatus;
}

/**
 * Expanded view of a single run, read whole when it opens (GET .../runs/{id})
 * and again whenever the change stream reports the automation's runs moved,
 * so the steps tick while the run is in flight.
 *
 * It shows what a person can read and nothing else: the run's outcome when
 * it did not succeed, the email it sent, and per-step cards that render the
 * agent's markdown ``final_message``. The run's raw records (its output with
 * ``final_message`` and token ``usage``, the structured error, the resolved
 * inputs, the artifact list and the definition snapshot) are the server's
 * internals: as JSON they repeated what the step cards say in words, or said
 * nothing a person could act on, so they are not shown.
 */
export function RunDetailsPanel({ automationId, runId, liveStatus }: RunDetailsPanelProps) {
	const isTerminal = liveStatus !== "pending" && liveStatus !== "running";
	const { data: run, isLoading, error } = useAutomationRun(automationId, runId);
	// The schema keeps step results as opaque records; RunStepResult is the typed view.
	const liveSteps = (run?.step_results ?? []) as RunStepResult[];

	const runError = run?.error && Object.keys(run.error).length > 0 ? run.error : null;
	const email = readRunEmail(run?.output);
	const heavyLoading = isTerminal && isLoading && !run;
	const heavyError = isTerminal && !!error;

	return (
		<div className="flex flex-col gap-4 border-t border-border/60 bg-muted/20 p-4">
			{runError ? <RunErrorSection error={runError} /> : null}

			{email ? (
				<p
					className={cn(
						"flex items-start gap-1.5 text-xs wrap-break-word",
						email.status !== "not_sent"
							? "text-muted-foreground"
							: "text-amber-800 dark:text-amber-300"
					)}
				>
					{email.status !== "not_sent" ? (
						<Mail className="mt-0.5 size-3.5 shrink-0" aria-hidden />
					) : (
						<MailX className="mt-0.5 size-3.5 shrink-0" aria-hidden />
					)}
					<span className="min-w-0">{runEmailSentence(email)}</span>
				</p>
			) : null}

			<Section icon={GitCommitHorizontal} label={`Step results · ${liveSteps.length}`}>
				{liveSteps.length === 0 ? (
					<p className="text-xs text-muted-foreground">
						{isTerminal ? "No steps recorded." : "Waiting for first step…"}
					</p>
				) : (
					<div className="flex flex-col gap-2">
						{liveSteps.map((step, index) => (
							<RunStepResultCard key={step.step_id ?? index} step={step} />
						))}
					</div>
				)}
			</Section>

			{heavyLoading ? (
				<Skeleton className="h-16 w-full" />
			) : heavyError ? (
				<p className="text-xs text-muted-foreground">
					Couldn't load run details{error?.message ? `: ${error.message}` : "."}
				</p>
			) : null}
		</div>
	);
}

/** Section heading per tone: only a real failure reads as an "Error". */
const SECTION_LABEL = {
	destructive: "Error",
	warning: "Needs attention",
	muted: "Outcome",
} as const;
const ALERT_VARIANT = { destructive: "destructive", warning: "warning", muted: "default" } as const;

/**
 * Run-level error: a readable alert, its title from the error's type and its
 * sentence when the server gave one. Needs attention and Timed out read as
 * warnings, Cancelled as a plain note, the rest as failures.
 */
function RunErrorSection({ error }: { error: Record<string, unknown> }) {
	const message = typeof error.message === "string" ? error.message : null;
	const type = typeof error.type === "string" ? error.type : undefined;
	const { title, tone } = describeRunError(type);
	const Icon = tone === "destructive" ? AlertCircle : AlertTriangle;
	const label =
		type === "TimedOut" || type === "StepTimedOut" || type === "timed_out"
			? "Timed out"
			: SECTION_LABEL[tone];

	return (
		<Section icon={Icon} label={label} tone={tone === "destructive" ? "destructive" : "default"}>
			<Alert variant={ALERT_VARIANT[tone]}>
				<Icon aria-hidden />
				<AlertTitle>{title}</AlertTitle>
				{message ? (
					<AlertDescription className="wrap-break-word">{message}</AlertDescription>
				) : null}
			</Alert>
		</Section>
	);
}

function Section({
	icon: Icon,
	label,
	tone = "default",
	children,
}: {
	icon: typeof AlertCircle;
	label: string;
	tone?: "default" | "destructive";
	children: React.ReactNode;
}) {
	return (
		<div className="flex flex-col gap-1.5">
			<div
				className={cn(
					"flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-wider",
					tone === "destructive" ? "text-destructive" : "text-muted-foreground"
				)}
			>
				<Icon className="size-3" aria-hidden />
				{label}
			</div>
			{children}
		</div>
	);
}
