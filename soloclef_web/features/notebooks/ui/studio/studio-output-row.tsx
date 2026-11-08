"use client";

import {
	Check,
	ExternalLink,
	MoreVertical,
	Pencil,
	Play,
	RotateCcw,
	Trash2,
	TriangleAlert,
	X,
} from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { cn } from "@/lib/utils";
import { formatDuration, isInFlight } from "../../lib/output-meta";
import { knownPhase } from "../../lib/output-phase";
import { relativeTime } from "../../lib/relative-time";
import { studioTile } from "../../lib/studio-catalog";
import type { StudioOutput } from "../../model/notebook";

interface StudioOutputRowProps {
	output: StudioOutput;
	onOpen: () => void;
	onRename: (title: string) => Promise<unknown>;
	onDelete: () => Promise<unknown>;
	onRetry: () => void;
}

type Mode = "idle" | "renaming" | "confirming-delete";

export function StudioOutputRow({
	output,
	onOpen,
	onRename,
	onDelete,
	onRetry,
}: StudioOutputRowProps) {
	const t = useTranslations("notebooks");
	const locale = useLocale();
	const [mode, setMode] = useState<Mode>("idle");
	const [draft, setDraft] = useState(output.title);
	const [busy, setBusy] = useState(false);
	const Icon = studioTile(output.kind).icon;
	const inFlight = isInFlight(output.status);
	const playable =
		output.status === "ready" && (output.kind === "audio" || output.kind === "video");

	const run = async (action: () => Promise<unknown>) => {
		setBusy(true);
		try {
			await action();
			setMode("idle");
		} finally {
			setBusy(false);
		}
	};

	const phase = inFlight && output.status === "running" ? knownPhase(output.phase) : null;
	const status = inFlight
		? phase
			? output.progress
				? t("phase_progress", { phase: t(`phases.${phase}`), progress: output.progress })
				: t(`phases.${phase}`)
			: output.progress
				? t("status_progress", { progress: output.progress })
				: t(`status.${output.status}`)
		: output.status === "ready"
			? null
			: t(`status.${output.status}`);
	const format = output.options.format
		? t(`options.${output.kind}.format.${String(output.options.format)}.label`)
		: output.kind === "infographic" && output.options.style
			? t(`options.infographic.style.${String(output.options.style)}.label`)
			: t(`kinds.${output.kind}.label`);
	const meta = [
		output.duration_seconds ? formatDuration(output.duration_seconds) : null,
		format,
		t("source_count", { count: output.source_count }),
		relativeTime(output.created_at, locale),
	]
		.filter(Boolean)
		.join(" · ");

	if (mode === "renaming") {
		return (
			<form
				className="flex items-center gap-2 rounded-lg px-2 py-1.5"
				onSubmit={(event) => {
					event.preventDefault();
					if (draft.trim()) void run(() => onRename(draft.trim()));
				}}
			>
				<Input
					autoFocus
					value={draft}
					onChange={(event) => setDraft(event.target.value)}
					onKeyDown={(event) => {
						if (event.key === "Escape") setMode("idle");
					}}
					maxLength={200}
					className="h-8"
					aria-label={t("rename")}
				/>
				<Button type="submit" size="icon" className="size-8" disabled={busy} aria-label={t("save")}>
					<Check className="size-4" />
				</Button>
				<Button
					type="button"
					variant="ghost"
					size="icon"
					className="size-8"
					onClick={() => setMode("idle")}
					aria-label={t("cancel")}
				>
					<X className="size-4" />
				</Button>
			</form>
		);
	}

	if (mode === "confirming-delete") {
		return (
			<div className="space-y-2 rounded-lg border px-3 py-2" role="alert">
				<p className="text-sm">{t("delete_output_confirm", { title: output.title })}</p>
				<div className="flex justify-end gap-2">
					<Button size="sm" variant="ghost" onClick={() => setMode("idle")} disabled={busy}>
						{t("cancel")}
					</Button>
					<Button size="sm" variant="destructive" onClick={() => run(onDelete)} disabled={busy}>
						{t("delete")}
					</Button>
				</div>
			</div>
		);
	}

	return (
		<div className="group flex min-w-0 items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-accent">
			<span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
				{inFlight ? <Spinner size="xs" /> : <Icon className="size-4" aria-hidden />}
			</span>
			<button
				type="button"
				onClick={onOpen}
				disabled={output.status !== "ready"}
				className="min-w-0 flex-1 text-left disabled:cursor-default"
			>
				<span className="block truncate text-sm font-medium">{output.title}</span>
				<span className="block truncate text-xs text-muted-foreground">
					{status ? (
						<span
							className={cn(
								output.status === "failed" && "text-destructive",
								"after:content-['_·_']"
							)}
						>
							{output.status === "failed" ? (
								<TriangleAlert className="mr-1 inline size-3" aria-hidden />
							) : null}
							{status}
						</span>
					) : null}
					{meta}
				</span>
			</button>
			{playable ? (
				<Button
					variant="ghost"
					size="icon"
					className="size-8 shrink-0 rounded-full"
					onClick={onOpen}
					aria-label={t("play")}
				>
					<Play className="size-4" />
				</Button>
			) : null}
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button
						variant="ghost"
						size="icon"
						className="size-8 shrink-0 rounded-full text-muted-foreground opacity-100 md:opacity-0 md:group-hover:opacity-100 data-[state=open]:opacity-100"
						aria-label={t("more_actions")}
					>
						<MoreVertical className="size-4" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end">
					{output.status === "ready" ? (
						<DropdownMenuItem onSelect={onOpen}>
							<ExternalLink className="size-4" />
							{t("open")}
						</DropdownMenuItem>
					) : null}
					{output.status === "failed" || output.status === "cancelled" ? (
						<DropdownMenuItem onSelect={onRetry}>
							<RotateCcw className="size-4" />
							{t("try_again")}
						</DropdownMenuItem>
					) : null}
					<DropdownMenuItem
						onSelect={() => {
							setDraft(output.title);
							setMode("renaming");
						}}
					>
						<Pencil className="size-4" />
						{t("rename")}
					</DropdownMenuItem>
					<DropdownMenuItem
						className="text-destructive focus:text-destructive"
						onSelect={() => setMode("confirming-delete")}
					>
						<Trash2 className="size-4" />
						{t("delete")}
					</DropdownMenuItem>
					{output.generated_by?.model ? (
						<>
							<DropdownMenuSeparator />
							<DropdownMenuLabel className="max-w-64 truncate text-xs font-normal text-muted-foreground">
								{t(output.generated_by.fallback ? "made_with_fallback" : "made_with", {
									model: output.generated_by.model,
								})}
							</DropdownMenuLabel>
						</>
					) : null}
				</DropdownMenuContent>
			</DropdownMenu>
		</div>
	);
}
