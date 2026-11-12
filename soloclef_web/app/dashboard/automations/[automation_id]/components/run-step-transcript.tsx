"use client";
import { ChevronRight, CornerDownRight, Wrench } from "lucide-react";
import { memo } from "react";
import { MarkdownViewer } from "@/components/markdown-viewer";
import {
	readableArgs,
	type TranscriptCall,
	type TranscriptEntry,
} from "@/lib/automations/run-transcript";

/**
 * A step's conversation, in the order it happened: what the assistant said,
 * each tool it called and what the tool answered.
 *
 * What the assistant said is shown in full, as it wrote it. A tool's
 * arguments and its answer are folded: they are what a person opens when
 * they want to know why the run said what it did, and an answer can be a
 * whole document.
 */
export const RunStepTranscript = memo(function RunStepTranscript({
	entries,
}: {
	entries: TranscriptEntry[];
}) {
	return (
		<ol className="flex flex-col gap-2">
			{entries.map((entry, index) => (
				// The transcript is written once, when the step ends, and never
				// reordered, so a position is a stable key.
				// biome-ignore lint/suspicious/noArrayIndexKey: see above
				<li key={index} className="min-w-0">
					<Entry entry={entry} />
				</li>
			))}
		</ol>
	);
});

function Entry({ entry }: { entry: TranscriptEntry }) {
	if (entry.role === "tool") {
		return (
			<Folded
				icon={<CornerDownRight className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />}
				label={entry.tool ? "Answer from" : "A tool's answer"}
				name={entry.tool}
				body={entry.content}
				empty="The tool answered nothing."
			/>
		);
	}
	return (
		<div className="flex flex-col gap-2">
			{entry.content.trim() ? (
				<div className="min-w-0 wrap-break-word rounded-md border border-border/60 bg-background px-3 py-2">
					{entry.role === "user" ? (
						<p className="mb-1 text-[11px] text-muted-foreground">Sent to the assistant</p>
					) : null}
					<MarkdownViewer content={entry.content} />
				</div>
			) : null}
			{entry.toolCalls.map((call, index) => (
				// biome-ignore lint/suspicious/noArrayIndexKey: calls of one message keep their order
				<Call key={index} call={call} />
			))}
		</div>
	);
}

function Call({ call }: { call: TranscriptCall }) {
	const args = readableArgs(call.args);
	return (
		<Folded
			icon={<Wrench className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />}
			label="Called"
			name={call.name}
			body={args}
			empty="Called with no arguments."
		/>
	);
}

/** One folded row: a line saying what happened, opening onto its text. */
function Folded({
	icon,
	label,
	name,
	body,
	empty,
}: {
	icon: React.ReactNode;
	label: string;
	name: string | null;
	body: string;
	empty: string;
}) {
	return (
		<details className="group rounded-md border border-border/60 bg-muted/30">
			<summary className="flex cursor-pointer list-none items-center gap-2 rounded-md px-3 py-1.5 text-xs text-muted-foreground outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 [&::-webkit-details-marker]:hidden">
				{icon}
				<span className="min-w-0 flex-1 truncate">
					{label}
					{name ? <span className="ml-1 font-mono text-foreground">{name}</span> : null}
				</span>
				<ChevronRight
					className="size-3.5 shrink-0 transition-transform group-open:rotate-90 motion-reduce:transition-none"
					aria-hidden
				/>
			</summary>
			{body.trim() ? (
				<pre className="max-h-64 overflow-auto border-t border-border/60 px-3 py-2 font-mono text-[11px] leading-relaxed whitespace-pre-wrap wrap-break-word text-foreground">
					{body}
				</pre>
			) : (
				<p className="border-t border-border/60 px-3 py-2 text-xs text-muted-foreground">{empty}</p>
			)}
		</details>
	);
}
