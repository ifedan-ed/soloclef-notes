"use client";

import { AlarmClock, AlertCircle, ExternalLink } from "lucide-react";
import Link from "next/link";
import { type ReactNode, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import type { AutomationCreateRequest } from "@/contracts/types/automation.types";
import type {
	AutomationDraft,
	AutomationDraftStatus,
} from "@/contracts/types/automation-draft.types";
import { describeCron } from "@/lib/automations/describe-cron";
import {
	type DraftFields,
	draftFieldProblems,
	draftFields,
	requestWithFields,
} from "@/lib/automations/draft-fields";
import { automationPath } from "@/lib/dashboard-paths";
import { cn } from "@/lib/utils";
import { AutomationDraftForm } from "./automation-draft-form";
import { useAutomationDraft } from "./use-automation-draft";

const TITLE: Record<AutomationDraftStatus, string> = {
	pending: "Automation draft",
	created: "Automation created",
	discarded: "Draft discarded",
	superseded: "Draft replaced",
};

const SUBTITLE: Record<AutomationDraftStatus, string> = {
	pending: "Nothing is saved yet. Check it, change anything, then create it.",
	created: "It is in your Automations.",
	discarded: "Nothing was saved.",
	superseded: "A newer draft below replaces this one.",
};

function Frame({ children, muted }: { children: ReactNode; muted?: boolean }) {
	return (
		<div
			className={cn(
				"my-4 w-full max-w-lg overflow-hidden rounded-2xl border bg-muted/30",
				muted && "opacity-80"
			)}
			data-testid="automation-draft-card"
		>
			{children}
		</div>
	);
}

const Divider = () => <div className="mx-4 h-px bg-border/50 sm:mx-5" />;

type TriggerLike = { type: string; params: Record<string, unknown> };

/** When a trigger runs, in words: the schedule as the builder describes it, or the folder. */
function whenItRuns(trigger: TriggerLike | undefined, folders: AutomationDraft["folders"]): string {
	if (!trigger) return "No trigger";
	const { params } = trigger;
	if (trigger.type === "schedule" && typeof params.cron === "string") {
		return `${describeCron(params.cron)} (${String(params.timezone ?? "")})`;
	}
	const filter = params.filter as { folder_id?: unknown } | undefined;
	const folder = folders.find((f) => f.id === filter?.folder_id)?.title;
	return folder ? `When a document arrives in ${folder}` : "When an event happens";
}

/**
 * What a settled card shows: for a created draft, the automation as Create
 * saved it (the person may have changed it on the card), else the draft.
 */
function settledSummary(draft: AutomationDraft): { name: string; when: string } | null {
	if (draft.status === "created") {
		return draft.automation
			? {
					name: draft.automation.name,
					when: whenItRuns(draft.automation.triggers[0], draft.folders),
				}
			: null;
	}
	return { name: draft.request.name, when: whenItRuns(draft.request.triggers[0], draft.folders) };
}

/**
 * The card for one automation the chat drafted. It shows the draft the
 * server holds (never the model's arguments), lets the person change every
 * field, and is the only way it becomes an automation: Create sends the
 * edited request through the ordinary create route with the draft's id.
 * Enter does nothing here, so a keystroke meant for the chat box can never
 * create one.
 */
export function AutomationDraftCard({ draftId }: { draftId: string }) {
	const { state, create, discard } = useAutomationDraft(draftId);

	if (state.phase === "loading") {
		return (
			<Frame>
				<div className="flex items-center gap-2 px-4 py-4 text-sm text-muted-foreground sm:px-5">
					<Spinner size="sm" />
					Loading the automation draft…
				</div>
			</Frame>
		);
	}
	if (state.phase === "unavailable") {
		return (
			<Frame muted>
				<div className="flex items-center gap-2 px-4 py-4 text-sm text-muted-foreground sm:px-5">
					<AlarmClock className="size-4 shrink-0" aria-hidden />
					This automation draft could not be loaded.
				</div>
			</Frame>
		);
	}

	const { draft, busy, refusal } = state;
	const status = draft.status;
	const summary = settledSummary(draft);
	return (
		<Frame muted={status === "discarded" || status === "superseded"}>
			<div className="flex items-start gap-3 px-4 pt-4 pb-3 select-none sm:px-5">
				<AlarmClock className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
				<div className="min-w-0">
					<p className="text-sm font-semibold text-foreground" data-testid="automation-draft-title">
						{TITLE[status]}
					</p>
					<p className="mt-0.5 text-xs text-muted-foreground">{SUBTITLE[status]}</p>
				</div>
			</div>
			<Divider />
			{status === "pending" ? (
				<PendingDraft
					draft={draft}
					busy={busy}
					refusal={refusal}
					onCreate={create}
					onDiscard={discard}
				/>
			) : (
				<div className="space-y-1 px-4 py-3 sm:px-5">
					{summary ? (
						<>
							<p className="break-words text-sm font-medium text-foreground">{summary.name}</p>
							<p className="text-xs text-muted-foreground">{summary.when}</p>
						</>
					) : (
						<p className="text-xs text-muted-foreground">The automation was deleted since.</p>
					)}
					{status === "created" && draft.automation_id !== null ? (
						<Link
							href={automationPath(draft.automation_id)}
							className="inline-flex items-center gap-1.5 pt-1 text-xs text-primary hover:underline"
							data-testid="automation-draft-open"
						>
							<ExternalLink className="h-3.5 w-3.5" aria-hidden />
							Open the automation
						</Link>
					) : null}
				</div>
			)}
		</Frame>
	);
}

function PendingDraft({
	draft,
	busy,
	refusal,
	onCreate,
	onDiscard,
}: {
	draft: AutomationDraft;
	busy: "create" | "discard" | null;
	refusal: string | null;
	onCreate: (request: AutomationCreateRequest) => void;
	onDiscard: () => void;
}) {
	// The fields the card can edit; null for a draft of another shape, which
	// is shown as it is and created as drafted.
	const initial = useMemo(() => draftFields(draft.request), [draft.request]);
	const [edited, setEdited] = useState<{ fields: DraftFields | null; problems: string[] }>(() => ({
		fields: initial,
		problems: initial ? draftFieldProblems(initial) : [],
	}));
	const request = initial
		? edited.fields
			? requestWithFields(draft.request, edited.fields)
			: null
		: draft.request;
	const blocked = request === null || edited.problems.length > 0;

	return (
		<>
			<div className="px-4 py-4 sm:px-5">
				{initial ? (
					<AutomationDraftForm
						draft={draft}
						initial={initial}
						disabled={busy !== null}
						onChange={(fields, problems) => setEdited({ fields, problems })}
					/>
				) : (
					<div className="space-y-1">
						<p className="break-words text-sm font-medium text-foreground">{draft.request.name}</p>
						<p className="text-xs text-muted-foreground">
							{whenItRuns(draft.request.triggers[0], draft.folders)}
						</p>
						<p className="text-xs text-muted-foreground">
							This draft cannot be edited here. To change it, ask in the chat.
						</p>
					</div>
				)}
			</div>
			{(refusal || edited.problems.length > 0) && (
				<div
					role="alert"
					className="mx-4 mb-3 space-y-1 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive sm:mx-5"
					data-testid="automation-draft-problems"
				>
					{[...(refusal ? [refusal] : []), ...edited.problems].map((problem) => (
						<p key={problem} className="flex items-start gap-1.5 break-words">
							<AlertCircle className="mt-0.5 h-3 w-3 shrink-0" aria-hidden />
							{problem}
						</p>
					))}
				</div>
			)}
			<Divider />
			<div className="flex flex-wrap items-center gap-2 px-4 py-3 select-none sm:px-5">
				<Button
					size="sm"
					className="rounded-lg"
					disabled={blocked || busy !== null}
					onClick={() => request && onCreate(request)}
					data-testid="automation-draft-create"
				>
					{busy === "create" ? <Spinner size="xs" /> : null}
					Create automation
				</Button>
				<Button
					size="sm"
					variant="ghost"
					className="rounded-lg text-muted-foreground"
					disabled={busy !== null}
					onClick={onDiscard}
					data-testid="automation-draft-discard"
				>
					Discard
				</Button>
			</div>
		</>
	);
}
