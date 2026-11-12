"use client";
import {
	AlertTriangle,
	Ban,
	CheckCircle2,
	ChevronDown,
	Hand,
	MinusCircle,
	NotebookPen,
	TimerOff,
	XCircle,
} from "lucide-react";
import Link from "next/link";
import { memo, useMemo, useState } from "react";
import { JsonView } from "@/components/json-view";
import { MarkdownViewer } from "@/components/markdown-viewer";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { ScrollArea } from "@/components/ui/scroll-area";
import type { RunStepResult } from "@/contracts/types/automation.types";
import { notePath } from "@/features/notes/lib/note-paths";
import { formatDuration } from "@/lib/automations/run-duration";
import { describeRunError } from "@/lib/automations/run-outcome";
import {
	countToolCalls,
	modelLine,
	readRefused,
	readSavedNote,
	readTranscript,
	transcriptForDisplay,
} from "@/lib/automations/run-transcript";
import { cn } from "@/lib/utils";
import { RunStepTranscript } from "./run-step-transcript";

type BadgeVariant = React.ComponentProps<typeof Badge>["variant"];

const STATUS_BADGE: Record<
	string,
	{ label: string; variant: BadgeVariant; icon: typeof CheckCircle2 }
> = {
	succeeded: { label: "Succeeded", variant: "outline", icon: CheckCircle2 },
	failed: { label: "Failed", variant: "destructive", icon: XCircle },
	needs_attention: { label: "Needs attention", variant: "secondary", icon: AlertTriangle },
	skipped: { label: "Skipped", variant: "secondary", icon: MinusCircle },
	timed_out: { label: "Timed out", variant: "secondary", icon: TimerOff },
	cancelled: { label: "Cancelled", variant: "secondary", icon: Ban },
};

const ALERT_VARIANT = { destructive: "destructive", warning: "warning", muted: "default" } as const;

function StepStatusBadge({ status }: { status: string }) {
	const meta = STATUS_BADGE[status] ?? {
		label: status,
		variant: "outline" as const,
		icon: MinusCircle,
	};
	const Icon = meta.icon;
	return (
		<Badge variant={meta.variant} className="shrink-0">
			<Icon aria-hidden />
			{meta.label}
		</Badge>
	);
}

/**
 * One step from a run's ``step_results``, outcome first: a step error as a
 * readable alert, the tools nobody was present to approve with the reason
 * each was not run, the agent's markdown ``final_message`` rendered, where
 * the result was saved, and then the transcript (what the assistant said,
 * called and was answered) behind a fold. The full structured payload stays
 * behind "View raw".
 */
export const RunStepResultCard = memo(function RunStepResultCard({
	step,
}: {
	step: RunStepResult;
}) {
	const [rawOpen, setRawOpen] = useState(false);
	const [transcriptOpen, setTranscriptOpen] = useState(false);

	const duration = formatDuration(step.started_at, step.finished_at);
	const attempts = step.attempts ?? 0;
	const finalMessage =
		typeof step.result?.final_message === "string" ? step.result.final_message : null;
	const errorMessage = step.error?.message;
	// A readable title for a step error (the raw exception name otherwise).
	const errorKind = describeRunError(
		step.status === "needs_attention" ? "NeedsAttention" : step.error?.type,
		"Error"
	);
	const ErrorIcon = errorKind.tone === "destructive" ? XCircle : AlertTriangle;
	const refused = useMemo(() => readRefused(step.result), [step.result]);
	const savedNote = useMemo(() => readSavedNote(step.result), [step.result]);
	const transcript = useMemo(
		() => transcriptForDisplay(readTranscript(step.result), finalMessage),
		[step.result, finalMessage]
	);
	const toolCalls = countToolCalls(transcript);
	const model = modelLine(step.result);
	const hasMeta = Boolean(duration) || attempts > 1 || Boolean(model);

	return (
		<Card className="border-border/60 shadow-none">
			<CardHeader className="gap-2 space-y-0 p-3">
				<div className="flex items-center justify-between gap-3">
					<div className="flex min-w-0 items-center gap-2">
						<span className="truncate font-mono text-xs font-medium">{step.action}</span>
						<span className="truncate text-xs text-muted-foreground">{step.step_id}</span>
					</div>
					<StepStatusBadge status={step.status} />
				</div>
				{hasMeta ? (
					<div className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-muted-foreground tabular-nums">
						{duration ? <span>{duration}</span> : null}
						{attempts > 1 ? <span>{attempts} attempts</span> : null}
						{model ? <span className="min-w-0 truncate">{model}</span> : null}
					</div>
				) : null}
			</CardHeader>

			<CardContent className="flex flex-col gap-3 p-3 pt-0">
				{errorMessage ? (
					<Alert variant={ALERT_VARIANT[errorKind.tone]}>
						<ErrorIcon aria-hidden />
						<AlertTitle>{errorKind.title}</AlertTitle>
						<AlertDescription className="wrap-break-word">{errorMessage}</AlertDescription>
					</Alert>
				) : null}

				{refused.length > 0 ? (
					<Alert variant="warning">
						<Hand aria-hidden />
						<AlertTitle>
							{refused.length === 1
								? "One tool was not run"
								: `${refused.length} tools were not run`}
						</AlertTitle>
						<AlertDescription>
							<ul className="flex flex-col gap-1.5">
								{refused.map((refusal, index) => (
									// The same tool can be refused twice in one step.
									// biome-ignore lint/suspicious/noArrayIndexKey: written once, never reordered
									<li key={index} className="wrap-break-word">
										<span className="font-mono text-foreground">{refusal.tool}</span>
										{refusal.reason ? <span className="block">{refusal.reason}</span> : null}
									</li>
								))}
							</ul>
						</AlertDescription>
					</Alert>
				) : null}

				{finalMessage ? (
					<div className="min-w-0 wrap-break-word rounded-md border border-border/60 bg-background px-3 py-2">
						<MarkdownViewer content={finalMessage} enableCitations />
					</div>
				) : null}

				{savedNote ? (
					<Link
						href={notePath(savedNote.documentId)}
						className="flex w-fit max-w-full items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground hover:underline"
					>
						<NotebookPen className="size-3.5 shrink-0" aria-hidden />
						<span className="min-w-0 truncate">
							Saved as a note: <span className="text-foreground">{savedNote.title}</span>
						</span>
					</Link>
				) : null}

				{transcript.length > 0 ? (
					<Collapsible open={transcriptOpen} onOpenChange={setTranscriptOpen}>
						<CollapsibleTrigger asChild>
							<Button
								type="button"
								variant="ghost"
								size="sm"
								className="h-7 w-fit px-2 text-xs text-muted-foreground"
								aria-expanded={transcriptOpen}
							>
								<ChevronDown
									className={cn(
										"transition-transform motion-reduce:transition-none",
										transcriptOpen && "rotate-180"
									)}
									aria-hidden
								/>
								{transcriptOpen ? "Hide transcript" : "Show transcript"}
								<span className="tabular-nums">
									·{" "}
									{toolCalls > 0
										? `${toolCalls} tool call${toolCalls === 1 ? "" : "s"}`
										: `${transcript.length} message${transcript.length === 1 ? "" : "s"}`}
								</span>
							</Button>
						</CollapsibleTrigger>
						<CollapsibleContent className="mt-2">
							<RunStepTranscript entries={transcript} />
						</CollapsibleContent>
					</Collapsible>
				) : null}

				<Collapsible open={rawOpen} onOpenChange={setRawOpen}>
					<CollapsibleTrigger asChild>
						<Button
							type="button"
							variant="ghost"
							size="sm"
							className="h-7 w-fit px-2 text-xs text-muted-foreground"
							aria-expanded={rawOpen}
						>
							<ChevronDown
								className={cn(
									"transition-transform motion-reduce:transition-none",
									rawOpen && "rotate-180"
								)}
								aria-hidden
							/>
							{rawOpen ? "Hide raw" : "View raw"}
						</Button>
					</CollapsibleTrigger>
					<CollapsibleContent>
						<ScrollArea className="mt-2 max-h-64 rounded-md bg-muted/40 px-3 py-2">
							<JsonView src={step} collapsed={1} />
						</ScrollArea>
					</CollapsibleContent>
				</Collapsible>
			</CardContent>
		</Card>
	);
});
