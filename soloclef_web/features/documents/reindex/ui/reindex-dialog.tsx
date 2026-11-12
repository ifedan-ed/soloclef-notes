"use client";

import { useMutation } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { useId, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectSeparator,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import type { DocumentParser } from "@/contracts/types/document.types";
import { PARSER_NAME_KEYS, unavailableReasonKey } from "@/features/parser-choice/parser-labels";
import { useEtlCapabilities } from "@/hooks/use-etl-capabilities";
import { resolveParserChoice } from "@/lib/etl/parser-choice";
import { type ReindexFailureKind, reindexDocument, reindexFailure } from "../reindex";

/** Radix reserves the empty string, so "leave the parser alone" needs a value of its own. */
const KEEP = "keep";

const FALLBACK_KEYS = {
	busy: "reindex_busy",
	unavailable: "reindex_unavailable",
	refused: "reindex_failed",
	failed: "reindex_failed",
} as const satisfies Record<ReindexFailureKind, string>;

interface ReindexDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	documentId: number;
	title: string;
	/** Called once the document is queued, so the list can show it indexing. */
	onQueued: () => void;
}

/**
 * Index a document again, optionally with a different parser.
 *
 * It starts on "keep the current parser": the common case is a document
 * that failed because something was down, and re-reading it should not
 * quietly move it to another service. A refusal stays in the dialog, in the
 * server's words, so the person can change the choice and try again.
 */
export function ReindexDialog({
	open,
	onOpenChange,
	documentId,
	title,
	onQueued,
}: ReindexDialogProps) {
	const t = useTranslations("notebooks");
	const parsers = useTranslations("upload_documents");
	const common = useTranslations("common");
	const selectId = useId();
	const [picked, setPicked] = useState<DocumentParser | typeof KEEP>(KEEP);
	// Asked for only once the dialog is open; cached for an hour after that.
	const capabilities = useEtlCapabilities();
	const options = resolveParserChoice(capabilities.data, undefined).options;

	const reindex = useMutation({
		// The dialog shows the refusal itself.
		meta: { suppressGlobalErrorToast: true },
		mutationFn: () => reindexDocument(documentId, picked === KEEP ? undefined : picked),
		onSuccess: () => {
			toast.success(t("reindex_queued"));
			onQueued();
			dismiss();
		},
		// When the queue is down the server marks the document failed; the
		// list should show that rather than the state from before.
		onError: () => onQueued(),
	});

	/** Close and forget the choice, so the next opening starts on "keep". */
	function dismiss() {
		setPicked(KEEP);
		reindex.reset();
		onOpenChange(false);
	}

	/** What the person's own closing does (Cancel, Escape, a click outside). */
	function close(next: boolean) {
		if (next) return onOpenChange(true);
		// A request in flight is not abandoned by closing: wait for its answer.
		if (!reindex.isPending) dismiss();
	}

	const failure = reindex.isError ? reindexFailure(reindex.error) : null;

	return (
		<Dialog open={open} onOpenChange={close}>
			<DialogContent className="max-w-md">
				<DialogHeader>
					<DialogTitle>{t("reindex_title")}</DialogTitle>
					<DialogDescription className="break-words">
						{t("reindex_description", { title })}
					</DialogDescription>
				</DialogHeader>
				<div className="space-y-1.5">
					<Label htmlFor={selectId}>{parsers("parser_label")}</Label>
					<Select
						value={picked}
						onValueChange={(value) => {
							setPicked(value as DocumentParser | typeof KEEP);
							// The refusal was about the previous choice.
							reindex.reset();
						}}
						disabled={reindex.isPending}
					>
						<SelectTrigger id={selectId} className="w-full">
							{/* Given outright: the list is mounted only once opened. */}
							<SelectValue>
								{picked === KEEP ? t("reindex_parser_keep") : parsers(PARSER_NAME_KEYS[picked])}
							</SelectValue>
						</SelectTrigger>
						<SelectContent>
							<SelectItem value={KEEP}>{t("reindex_parser_keep")}</SelectItem>
							{options.length > 0 ? <SelectSeparator /> : null}
							{/* All four are listed; one the server has no key for says
							    so and cannot be picked. */}
							{options.map((option) => (
								<SelectItem
									key={option.parser}
									value={option.parser}
									disabled={!option.available}
									description={
										option.available
											? undefined
											: parsers(unavailableReasonKey(option.unavailableReason))
									}
								>
									{parsers(PARSER_NAME_KEYS[option.parser])}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
					<p className="text-xs text-muted-foreground">
						{capabilities.isError
							? t("reindex_parsers_unavailable")
							: picked === KEEP
								? t("reindex_parser_hint")
								: t("reindex_parser_change", { parser: parsers(PARSER_NAME_KEYS[picked]) })}
					</p>
				</div>
				{failure ? (
					<p
						role="alert"
						className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive"
					>
						{failure.sentence ?? t(FALLBACK_KEYS[failure.kind])}
					</p>
				) : null}
				<DialogFooter>
					<Button variant="outline" onClick={() => close(false)} disabled={reindex.isPending}>
						{common("cancel")}
					</Button>
					<Button onClick={() => reindex.mutate()} disabled={reindex.isPending}>
						{reindex.isPending ? <Spinner size="xs" /> : null}
						{t("reindex_submit")}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
