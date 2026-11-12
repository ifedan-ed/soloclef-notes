"use client";

import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import {
	fetchMemoryEditorDocument,
	getMemoryLimitState,
	resetMemory,
	saveMemoryMarkdown,
} from "@/components/editor-panel/memory";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
	AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { queryClient } from "@/lib/query-client/client";
import { cn } from "@/lib/utils";

/**
 * What the assistant remembers about the person between chats, as the
 * Markdown it keeps. The assistant writes to it during chats; here the
 * person reads it, corrects it, or empties it.
 */
export function MemorySettings() {
	const memory = useQuery({
		queryKey: cacheKeys.user.memory(),
		queryFn: ({ signal }) => fetchMemoryEditorDocument({ signal }),
	});
	const [draft, setDraft] = useState("");
	const [busy, setBusy] = useState<"save" | "reset" | null>(null);
	const saved = memory.data?.document.source_markdown ?? "";

	useEffect(() => {
		if (memory.data) setDraft(memory.data.document.source_markdown);
	}, [memory.data]);

	if (memory.isLoading) {
		return <Skeleton className="h-64 w-full" />;
	}
	if (memory.isError || !memory.data) {
		return (
			<div className="flex flex-col items-center gap-3 py-8 text-center">
				<p className="text-sm text-destructive">Could not load your memory.</p>
				<Button variant="outline" size="sm" onClick={() => memory.refetch()}>
					Retry
				</Button>
			</div>
		);
	}

	const limit = getMemoryLimitState(draft.length, memory.data.limits);
	const hasChanges = draft !== saved;

	// The server's answer is what is stored now; it replaces the cached copy.
	const store = (markdown: string) =>
		queryClient.setQueryData(cacheKeys.user.memory(), {
			limits: memory.data?.limits ?? null,
			document: { ...memory.data.document, source_markdown: markdown },
		});

	const save = async () => {
		setBusy("save");
		try {
			const { markdown } = await saveMemoryMarkdown({ markdown: draft });
			store(markdown);
			toast.success("Memory saved");
		} catch (error) {
			toast.error(error instanceof Error ? error.message : "Could not save your memory");
		} finally {
			setBusy(null);
		}
	};

	const forget = async () => {
		setBusy("reset");
		try {
			const { markdown } = await resetMemory();
			store(markdown);
			toast.success("Memory emptied");
		} catch (error) {
			toast.error(error instanceof Error ? error.message : "Could not empty your memory");
		} finally {
			setBusy(null);
		}
	};

	return (
		<div className="space-y-4">
			<p className="text-sm text-muted-foreground">
				The assistant notes lasting facts about you here (your field, how you like answers, what you
				are working on) and reads them at the start of every chat. Correct anything that is wrong;
				it is only ever used for your own chats.
			</p>
			<div className="space-y-2">
				<Label htmlFor="memory-markdown">Memory</Label>
				<Textarea
					id="memory-markdown"
					value={draft}
					onChange={(event) => setDraft(event.target.value)}
					placeholder="Nothing yet. The assistant adds to this as you chat."
					rows={14}
					className="min-h-72 resize-y font-mono text-xs md:text-sm"
					aria-invalid={limit.isOverLimit || undefined}
				/>
				<p
					className={cn(
						"text-xs",
						limit.level === "error"
							? "text-destructive"
							: limit.level === "warning"
								? "text-amber-600 dark:text-amber-400"
								: "text-muted-foreground"
					)}
				>
					{limit.label}
				</p>
			</div>
			<div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-between">
				<AlertDialog>
					<AlertDialogTrigger asChild>
						<Button variant="ghost" disabled={busy !== null || saved.length === 0}>
							Forget everything
						</Button>
					</AlertDialogTrigger>
					<AlertDialogContent>
						<AlertDialogHeader>
							<AlertDialogTitle>Empty your memory?</AlertDialogTitle>
							<AlertDialogDescription>
								The assistant forgets everything it noted about you. Your notebooks, notes and chats
								are not touched. This cannot be undone.
							</AlertDialogDescription>
						</AlertDialogHeader>
						<AlertDialogFooter>
							<AlertDialogCancel>Cancel</AlertDialogCancel>
							<AlertDialogAction onClick={() => void forget()}>Forget everything</AlertDialogAction>
						</AlertDialogFooter>
					</AlertDialogContent>
				</AlertDialog>
				<Button
					onClick={() => void save()}
					disabled={!hasChanges || busy !== null || limit.isOverLimit}
					className="gap-2"
				>
					{busy === "save" ? <Spinner size="sm" /> : null}
					Save memory
				</Button>
			</div>
		</div>
	);
}
