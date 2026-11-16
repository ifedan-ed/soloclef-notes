"use client";
import { ChevronDown, ChevronRight, Hand } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { RunSummary } from "@/contracts/types/automation.types";
import { formatDuration } from "@/lib/automations/run-duration";
import { isRunInFlight } from "@/lib/automations/run-outcome";
import { formatRelativeDate } from "@/lib/format-date";
import { CancelRunButton } from "./cancel-run-button";
import { RunDetailsPanel } from "./run-details-panel";
import { RunStatusBadge } from "./run-status-badge";

interface RunRowProps {
	run: RunSummary;
	automationId: number;
	/** Show Cancel while the run is waiting or running (``automations:execute``). */
	/** The run a link named: shown expanded and scrolled into view. */
	initiallyOpen?: boolean;
}

/**
 * One run row. Click to expand → renders the details panel inline.
 * The status comes from the parent's runs list, which the change stream
 * keeps current; the panel reads the run in full only once expanded. A run
 * still waiting or running gets a Cancel beside the toggle.
 */
export function RunRow({ run, automationId, initiallyOpen = false }: RunRowProps) {
	const [open, setOpen] = useState(initiallyOpen);
	const rowRef = useRef<HTMLDivElement>(null);
	// biome-ignore lint/correctness/useExhaustiveDependencies: once, when the row first shows
	useEffect(() => {
		if (!initiallyOpen) return;
		const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
		rowRef.current?.scrollIntoView({ block: "start", behavior: still ? "auto" : "smooth" });
	}, []);
	const duration = formatDuration(run.started_at, run.finished_at);
	const startedLabel = run.started_at
		? formatRelativeDate(run.started_at)
		: formatRelativeDate(run.created_at);

	return (
		<div ref={rowRef} className="rounded-md border border-border/60 overflow-hidden scroll-mt-4">
			<div className="flex items-center hover:bg-muted/30 transition-colors">
				<button
					type="button"
					onClick={() => setOpen((value) => !value)}
					className="flex min-w-0 flex-1 flex-wrap items-center justify-between gap-x-4 gap-y-1 px-4 py-3 text-left"
					aria-expanded={open}
				>
					{/* The two halves wrap onto two lines when the row is narrow:
					    side by side at 390px, the time had a few pixels and read
					    "1." instead of "1 minute ago". */}
					<div className="flex min-w-0 items-center gap-3">
						{open ? (
							<ChevronDown className="h-4 w-4 text-muted-foreground shrink-0" aria-hidden />
						) : (
							<ChevronRight className="h-4 w-4 text-muted-foreground shrink-0" aria-hidden />
						)}
						<RunStatusBadge status={run.status} />
						<span className="text-xs text-muted-foreground truncate">{startedLabel}</span>
					</div>
					<div className="flex shrink-0 items-center gap-3 pl-7 text-xs text-muted-foreground sm:pl-0">
						{duration && <span className="font-mono">{duration}</span>}
						<TriggerSource triggerId={run.trigger_id ?? null} />
					</div>
				</button>
				{isRunInFlight(run.status) && (
					<div className="pr-2">
						<CancelRunButton automationId={automationId} runId={run.id} />
					</div>
				)}
			</div>

			{open && (
				<RunDetailsPanel automationId={automationId} runId={run.id} liveStatus={run.status} />
			)}
		</div>
	);
}

function TriggerSource({ triggerId }: { triggerId: number | null }) {
	if (triggerId == null) {
		return (
			<span className="inline-flex items-center gap-1">
				<Hand className="h-3 w-3" aria-hidden />
				Manual
			</span>
		);
	}
	return <span>via trigger #{triggerId}</span>;
}
