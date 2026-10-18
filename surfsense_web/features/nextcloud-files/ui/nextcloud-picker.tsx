"use client";

import { useQueryClient } from "@tanstack/react-query";
import { Search, X } from "lucide-react";
import { useTranslations } from "next-intl";
import { useId, useMemo, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import type { DocumentParser } from "@/contracts/types/document.types";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { useEtlCapabilities } from "@/hooks/use-etl-capabilities";
import { acceptedExtensions, resolveParserChoice } from "@/lib/etl/parser-choice";
import {
	type ImportPreview,
	type ImportResult,
	nextcloudFilesApi,
	type PickedItem,
} from "../api/nextcloud-files-api";
import { useNextcloudListing } from "../hooks/use-nextcloud-listing";
import { importErrorKind } from "../lib/import-errors";
import {
	countPicked,
	isPickable,
	type Selection,
	toggleAll,
	toggleEntry,
	withoutUnreadable,
} from "../lib/picker-selection";
import { NextcloudBreadcrumbs } from "./nextcloud-breadcrumbs";
import { NextcloudFileList } from "./nextcloud-file-list";
import { NextcloudImportConfirm } from "./nextcloud-import-confirm";

interface NextcloudPickerProps {
	connectorId: number;
	/** Link what is imported to this notebook. */
	notebookId?: number;
	onImported: (result: ImportResult) => void;
}

/**
 * Browse the connected Nextcloud and tick files or folders; "Add" imports them
 * once with the chosen parser. Nothing is synced afterwards.
 */
export function NextcloudPicker({ connectorId, notebookId, onImported }: NextcloudPickerProps) {
	const t = useTranslations("nextcloud_files");
	const tUpload = useTranslations("upload_documents");
	const [path, setPath] = useState("/");
	const [query, setQuery] = useState("");
	const [selection, setSelection] = useState<Selection>(new Map());
	const [chosenParser, setChosenParser] = useState<DocumentParser | undefined>();
	// As on Upload files: the vision model describes images (and the pictures
	// inside documents) instead of leaving them to the parser's OCR alone.
	const [useVision, setUseVision] = useState(false);
	const [preview, setPreview] = useState<ImportPreview | null>(null);
	const [busy, setBusy] = useState(false);
	const selectAllId = useId();
	const queryClient = useQueryClient();

	const { data: capabilities } = useEtlCapabilities();
	const parserChoice = resolveParserChoice(capabilities, chosenParser);
	const parser = parserChoice.effectiveParser;
	const acceptedFor = (choice: DocumentParser | undefined, vision: boolean) =>
		capabilities ? acceptedExtensions(capabilities, choice, { visionRequested: vision }) : null;
	const accepted = useMemo(
		() =>
			capabilities
				? acceptedExtensions(capabilities, parser, { visionRequested: useVision })
				: null,
		[capabilities, parser, useVision]
	);
	const parserName = parser === "cloud" ? tUpload("parser_cloud") : tUpload("parser_local");

	const listing = useNextcloudListing(connectorId, path, query);
	const showSpinner = useDelayedFlag(listing.isFetching);
	const picked = countPicked(selection);
	const total = picked.files + picked.folders;
	const pickable = listing.entries.filter((entry) => isPickable(entry, accepted));
	const allPicked = pickable.length > 0 && pickable.every((entry) => selection.has(entry.path));

	const openFolder = (next: string) => {
		setQuery("");
		setPath(next);
	};

	const changeParser = (value: string) => {
		const next = value as DocumentParser;
		setChosenParser(next);
		const nextAccepted = acceptedFor(next, useVision);
		if (nextAccepted) setSelection((current) => withoutUnreadable(current, nextAccepted));
		setPreview(null);
	};

	const changeVision = (next: boolean) => {
		setUseVision(next);
		const nextAccepted = acceptedFor(parser, next);
		if (nextAccepted) setSelection((current) => withoutUnreadable(current, nextAccepted));
		setPreview(null);
	};

	const reportError = (error: unknown) => {
		const kind = importErrorKind(error);
		toast.error(
			kind === "auth"
				? t("error_auth")
				: kind === "too_many"
					? t("error_too_many")
					: kind === "not_found"
						? t("error_not_found")
						: t("error_import")
		);
	};

	const items = (): PickedItem[] => Array.from(selection.values());

	const runImport = async () => {
		setBusy(true);
		try {
			const result = await nextcloudFilesApi.import(connectorId, {
				items: items(),
				parser,
				use_vision_llm: useVision,
				notebook_id: notebookId,
			});
			const added = result.document_ids.length;
			toast.success(t("imported", { count: added }), {
				description:
					result.unsupported.length + result.too_large.length > 0
						? t("confirm_skipped", { count: result.unsupported.length + result.too_large.length })
						: undefined,
			});
			setSelection(new Map());
			setPreview(null);
			onImported(result);
			// Newly picked files can now be refreshed from their menus.
			void queryClient.invalidateQueries({ queryKey: ["nextcloud-refreshable"] });
		} catch (error) {
			reportError(error);
		} finally {
			setBusy(false);
		}
	};

	const add = async () => {
		if (picked.folders === 0) {
			await runImport();
			return;
		}
		setBusy(true);
		try {
			setPreview(
				await nextcloudFilesApi.preview(connectorId, {
					items: items(),
					parser,
					use_vision_llm: useVision,
				})
			);
		} catch (error) {
			reportError(error);
		} finally {
			setBusy(false);
		}
	};

	const listError = listing.error ? importErrorKind(listing.error) : null;

	return (
		// Natural height, never shrunk below its content: squeezed into a short
		// phone dialog, the footer spilled over the note under the picker. The
		// list scrolls inside a screen-relative height; the tab scrolls the rest.
		<div className="flex flex-col gap-3">
			<div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
				<NextcloudBreadcrumbs path={path} onNavigate={openFolder} />
				<div className="relative sm:w-56">
					<Search
						aria-hidden="true"
						className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
					/>
					<Input
						value={query}
						onChange={(event) => setQuery(event.target.value)}
						placeholder={t("search_placeholder")}
						aria-label={t("search_placeholder")}
						className="h-9 pl-8 pr-8"
					/>
					{query ? (
						<button
							type="button"
							onClick={() => setQuery("")}
							aria-label={t("clear_search")}
							className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-0.5 text-muted-foreground hover:text-foreground"
						>
							<X className="size-3.5" />
						</button>
					) : null}
				</div>
			</div>

			<div className="flex flex-wrap items-center justify-between gap-2">
				<div className="flex items-center gap-2 text-sm">
					<Checkbox
						id={selectAllId}
						checked={allPicked}
						disabled={pickable.length === 0}
						onCheckedChange={() =>
							setSelection((current) => toggleAll(current, listing.entries, accepted))
						}
					/>
					<label htmlFor={selectAllId}>{t("select_all")}</label>
				</div>
				{parserChoice.options.length > 1 ? (
					<SegmentedControl
						value={parser ?? "local"}
						onValueChange={changeParser}
						ariaLabel={tUpload("parser_label")}
						options={parserChoice.options.map((option) => ({
							value: option.parser,
							label: option.parser === "cloud" ? tUpload("parser_cloud") : tUpload("parser_local"),
							disabled: !option.available,
						}))}
					/>
				) : null}
			</div>
			{parserChoice.options.length > 1 ? (
				<p className="text-xs text-muted-foreground">
					{parser === "cloud" ? tUpload("parser_cloud_desc") : tUpload("parser_local_desc")}
				</p>
			) : null}
			<div className="flex items-center justify-between gap-3 rounded-lg border px-3 py-2">
				<div className="min-w-0">
					<p className="text-sm font-medium">{tUpload("vision_llm_label")}</p>
					<p className="text-xs text-muted-foreground">{tUpload("vision_llm_desc")}</p>
				</div>
				<Switch
					checked={useVision}
					onCheckedChange={changeVision}
					aria-label={tUpload("vision_llm_label")}
				/>
			</div>

			<div
				className="relative h-[45dvh] max-h-96 min-h-40 overflow-y-auto rounded-lg border"
				aria-busy={listing.isFetching}
			>
				{showSpinner ? (
					<div className="absolute right-2 top-2">
						<Spinner size="sm" />
					</div>
				) : null}
				{listError ? (
					<p className="p-4 text-sm text-muted-foreground">
						{listError === "auth"
							? t("error_auth")
							: listError === "not_found"
								? t("error_not_found")
								: t("error_browse")}
					</p>
				) : listing.entries.length === 0 && !listing.isLoading ? (
					<p className="p-4 text-sm text-muted-foreground">
						{listing.searching ? t("no_results") : t("empty_folder")}
					</p>
				) : (
					<NextcloudFileList
						entries={listing.entries}
						selection={selection}
						accepted={accepted}
						parserName={parserName}
						showLocation={listing.searching}
						onToggle={(entry) => {
							setPreview(null);
							setSelection((current) => toggleEntry(current, entry));
						}}
						onOpenFolder={openFolder}
					/>
				)}
			</div>

			{preview ? (
				<NextcloudImportConfirm
					preview={preview}
					busy={busy}
					onConfirm={runImport}
					onCancel={() => setPreview(null)}
				/>
			) : (
				<div className="flex flex-wrap items-center justify-between gap-2">
					<p className="text-xs text-muted-foreground">
						{total > 0
							? t("picked", { files: picked.files, folders: picked.folders })
							: t("once_note")}
					</p>
					<Button disabled={total === 0 || busy} onClick={add}>
						{t("add_n_sources", { count: total })}
					</Button>
				</div>
			)}
		</div>
	);
}
