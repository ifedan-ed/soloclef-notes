"use client";

import { useAtom } from "jotai";
import { ChevronDown, Cloud, Dot, File as FileIcon, FolderOpen, HardDrive, X } from "lucide-react";

import { useTranslations } from "next-intl";
import { type ChangeEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useDropzone } from "react-dropzone";
import { toast } from "sonner";
import { uploadDocumentMutationAtom } from "@/atoms/documents/document-mutation.atoms";
import { useRuntimeConfig } from "@/components/providers/runtime-config";
import {
	Accordion,
	AccordionContent,
	AccordionItem,
	AccordionTrigger,
} from "@/components/ui/accordion";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Progress } from "@/components/ui/progress";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import type { DocumentParser } from "@/contracts/types/document.types";
import { ParserCreditHint } from "@/features/billing/ui/parser-credit-hint";
import { useEtlCapabilities } from "@/hooks/use-etl-capabilities";
import { useElectronAPI } from "@/hooks/use-platform";
import { documentsApiService } from "@/lib/apis/documents-api.service";
import { splitDroppedFolders } from "@/lib/drop/dropped-files";
import {
	acceptedExtensions,
	partitionByExtension,
	resolveParserChoice,
} from "@/lib/etl/parser-choice";
import { trackDocumentUploadStarted } from "@/lib/posthog/events";
import { getAcceptedFileTypes, getSupportedExtensionsSet } from "@/lib/supported-extensions";

/** What an upload produced, for callers that link the new documents somewhere. */
export interface DocumentUploadResult {
	documentIds: number[];
	folderId: number | null;
}

interface DocumentUploadTabProps {
	workspaceId: string;
	onSuccess?: (result: DocumentUploadResult) => void;
	onAccordionStateChange?: (isExpanded: boolean) => void;
	/**
	 * Files dropped somewhere else (a notebook's Sources panel or chat) that
	 * open this tab pre-filled, so the parser and vision choices are made
	 * before anything uploads. Read once, when the tab mounts; remount it (a
	 * new ``key``) for the next drop.
	 */
	initialFiles?: File[];
}

interface FileWithId {
	id: string;
	file: File;
}

interface FolderEntry {
	id: string;
	file: File;
	relativePath: string;
}

interface FolderUploadData {
	folderName: string;
	entries: FolderEntry[];
}

interface FolderTreeNode {
	name: string;
	isFolder: boolean;
	size?: number;
	children: FolderTreeNode[];
}

function buildFolderTree(entries: FolderEntry[]): FolderTreeNode[] {
	const root: FolderTreeNode = { name: "", isFolder: true, children: [] };

	for (const entry of entries) {
		const parts = entry.relativePath.split("/");
		let current = root;

		for (let i = 0; i < parts.length - 1; i++) {
			let child = current.children.find((c) => c.name === parts[i] && c.isFolder);
			if (!child) {
				child = { name: parts[i], isFolder: true, children: [] };
				current.children.push(child);
			}
			current = child;
		}

		current.children.push({
			name: parts[parts.length - 1],
			isFolder: false,
			size: entry.file.size,
			children: [],
		});
	}

	function sortNodes(node: FolderTreeNode) {
		node.children.sort((a, b) => {
			if (a.isFolder !== b.isFolder) return a.isFolder ? -1 : 1;
			return a.name.localeCompare(b.name);
		});
		for (const child of node.children) sortNodes(child);
	}
	sortNodes(root);

	return root.children;
}

function flattenTree(
	nodes: FolderTreeNode[],
	depth = 0,
	parentPath = ""
): { name: string; isFolder: boolean; depth: number; path: string; size?: number }[] {
	const items: { name: string; isFolder: boolean; depth: number; path: string; size?: number }[] =
		[];
	for (const node of nodes) {
		const path = parentPath ? `${parentPath}/${node.name}` : node.name;
		items.push({ name: node.name, isFolder: node.isFolder, depth, path, size: node.size });
		if (node.isFolder && node.children.length > 0) {
			items.push(...flattenTree(node.children, depth + 1, path));
		}
	}
	return items;
}

const FOLDER_BATCH_SIZE_BYTES = 20 * 1024 * 1024;
const FOLDER_BATCH_MAX_FILES = 10;

const UNAVAILABLE_REASON_KEYS = {
	missing_api_key: "parser_unavailable_missing_api_key",
	not_installed: "parser_unavailable_not_installed",
	unknown_service: "parser_unavailable_unknown_service",
} as const;

function unavailableReasonKey(reason: string | null) {
	return (
		UNAVAILABLE_REASON_KEYS[reason as keyof typeof UNAVAILABLE_REASON_KEYS] ??
		UNAVAILABLE_REASON_KEYS.unknown_service
	);
}

interface RejectedNotice {
	parser: DocumentParser | undefined;
	names: string[];
}

const toggleRowClass =
	"flex items-center justify-between rounded-lg bg-slate-400/5 dark:bg-white/5 p-3";

export function DocumentUploadTab({
	workspaceId,
	onSuccess,
	onAccordionStateChange,
	initialFiles,
}: DocumentUploadTabProps) {
	const t = useTranslations("upload_documents");
	const { etlService, maxFileSizeMB } = useRuntimeConfig();
	const [files, setFiles] = useState<FileWithId[]>([]);
	const [uploadProgress, setUploadProgress] = useState(0);
	const [accordionValue, setAccordionValue] = useState<string>("");
	const [useVisionLlm, setUseVisionLlm] = useState(false);
	const [selectedParser, setSelectedParser] = useState<DocumentParser | undefined>(undefined);
	const [rejectedNotice, setRejectedNotice] = useState<RejectedNotice | null>(null);
	// The backend owns which parser can run, what it costs and which files it
	// opens; until it answers nothing is offered and the server default applies.
	const { data: etlCapabilities, error: etlCapabilitiesError } = useEtlCapabilities();
	const parserChoice = resolveParserChoice(etlCapabilities, selectedParser);
	const activeParser = parserChoice.effectiveParser;
	const [uploadDocumentMutation] = useAtom(uploadDocumentMutationAtom);
	const { mutate: uploadDocuments, isPending: isUploading } = uploadDocumentMutation;
	const fileInputRef = useRef<HTMLInputElement>(null);
	const folderInputRef = useRef<HTMLInputElement>(null);
	const progressIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
	const [folderUpload, setFolderUpload] = useState<FolderUploadData | null>(null);
	const [isFolderUploading, setIsFolderUploading] = useState(false);
	const maxFileSizeBytes = useMemo(() => maxFileSizeMB * 1024 * 1024, [maxFileSizeMB]);

	useEffect(() => {
		return () => {
			if (progressIntervalRef.current) {
				clearInterval(progressIntervalRef.current);
			}
		};
	}, []);

	const electronAPI = useElectronAPI();
	const isElectron = !!electronAPI?.browseFiles;

	const acceptedFor = useCallback(
		(parser: DocumentParser | undefined, visionRequested: boolean) =>
			etlCapabilities
				? acceptedExtensions(etlCapabilities, parser, { visionRequested })
				: getSupportedExtensionsSet(getAcceptedFileTypes(etlService)),
		[etlCapabilities, etlService]
	);
	const supportedExtensionsSet = useMemo(
		() => acceptedFor(activeParser, useVisionLlm),
		[acceptedFor, activeParser, useVisionLlm]
	);
	const supportedExtensions = useMemo(
		() => Array.from(supportedExtensionsSet).sort(),
		[supportedExtensionsSet]
	);

	const parserName = useCallback(
		(parser: DocumentParser | undefined) =>
			(parser ?? etlCapabilities?.default_parser) === "cloud"
				? t("parser_cloud")
				: t("parser_local"),
		[etlCapabilities?.default_parser, t]
	);

	/** Drop already-selected files the new parser or vision setting cannot read. */
	const reapplyAcceptance = useCallback(
		(parser: DocumentParser | undefined, visionRequested: boolean) => {
			const accepted = acceptedFor(parser, visionRequested);
			const fileSplit = partitionByExtension(
				files.map((entry) => ({ entry, name: entry.file.name })),
				accepted
			);
			const folderSplit = partitionByExtension(
				(folderUpload?.entries ?? []).map((entry) => ({ entry, name: entry.file.name })),
				accepted
			);
			const rejected = [...fileSplit.rejected, ...folderSplit.rejected].map((item) => item.name);
			if (fileSplit.rejected.length) setFiles(fileSplit.accepted.map((item) => item.entry));
			if (folderUpload && folderSplit.rejected.length) {
				setFolderUpload(
					folderSplit.accepted.length
						? { ...folderUpload, entries: folderSplit.accepted.map((item) => item.entry) }
						: null
				);
			}
			setRejectedNotice(rejected.length ? { parser, names: rejected } : null);
		},
		[acceptedFor, files, folderUpload]
	);

	const handleParserChange = useCallback(
		(parser: DocumentParser) => {
			setSelectedParser(parser);
			reapplyAcceptance(parser, useVisionLlm);
		},
		[reapplyAcceptance, useVisionLlm]
	);

	const handleVisionChange = useCallback(
		(enabled: boolean) => {
			setUseVisionLlm(enabled);
			reapplyAcceptance(activeParser, enabled);
		},
		[reapplyAcceptance, activeParser]
	);

	const addFiles = useCallback(
		(dropped: File[]) => {
			const { accepted: incoming, rejected } = partitionByExtension(
				dropped,
				supportedExtensionsSet
			);
			setRejectedNotice(
				rejected.length ? { parser: activeParser, names: rejected.map((f) => f.name) } : null
			);
			const oversized = incoming.filter((f) => f.size > maxFileSizeBytes);
			if (oversized.length > 0) {
				toast.error(t("file_too_large"), {
					description: t("file_too_large_desc", {
						name: oversized[0].name,
						maxMB: maxFileSizeMB,
					}),
				});
			}
			const valid = incoming.filter((f) => f.size <= maxFileSizeBytes);
			if (valid.length === 0) return;

			setFolderUpload(null);
			setFiles((prev) => {
				const newEntries = valid.map((f) => ({
					id: crypto.randomUUID?.() ?? `file-${Date.now()}-${Math.random().toString(36)}`,
					file: f,
				}));
				return [...prev, ...newEntries];
			});
		},
		[t, maxFileSizeMB, maxFileSizeBytes, supportedExtensionsSet, activeParser]
	);

	const onDrop = useCallback(
		(acceptedFiles: File[]) => {
			addFiles(acceptedFiles);
		},
		[addFiles]
	);

	// A drop made outside the tab: one folder keeps its structure (a folder
	// upload), anything else is added as files. Waits for the parser
	// capabilities so the files are checked against the default parser the
	// tab will actually use.
	const seededRef = useRef(false);
	const capabilitiesSettled = etlCapabilities !== undefined || etlCapabilitiesError !== null;
	useEffect(() => {
		if (seededRef.current || !initialFiles?.length || !capabilitiesSettled) return;
		seededRef.current = true;
		const { loose, folders } = splitDroppedFolders(initialFiles);
		if (loose.length === 0 && folders.length === 1) {
			const [folder] = folders;
			const { accepted, rejected } = partitionByExtension(
				folder.entries.map((entry) => ({ entry, name: entry.file.name })),
				supportedExtensionsSet
			);
			setRejectedNotice(
				rejected.length
					? {
							parser: activeParser,
							names: rejected.map((item) => `${folder.name}/${item.entry.relativePath}`),
						}
					: null
			);
			if (accepted.length > 0) {
				setFiles([]);
				setFolderUpload({
					folderName: folder.name,
					entries: accepted.map(({ entry }) => ({
						id: crypto.randomUUID?.() ?? `file-${Date.now()}-${Math.random().toString(36)}`,
						file: entry.file,
						relativePath: entry.relativePath,
					})),
				});
			}
			return;
		}
		addFiles(initialFiles);
	}, [initialFiles, capabilitiesSettled, addFiles, supportedExtensionsSet, activeParser]);

	const { getRootProps, getInputProps, isDragActive } = useDropzone({
		// Type filtering happens in addFiles, which says what it turned away.
		onDrop,
		maxSize: maxFileSizeBytes,
		noClick: isElectron,
	});

	const handleFileInputClick = useCallback((e: React.MouseEvent<HTMLInputElement>) => {
		e.stopPropagation();
	}, []);

	const handleBrowseFiles = useCallback(async () => {
		if (!electronAPI?.browseFiles) return;

		const paths = await electronAPI.browseFiles();
		if (!paths || paths.length === 0) return;

		const fileDataList = await electronAPI.readLocalFiles(paths);
		const filtered = fileDataList.filter(
			(fd: { name: string; data: ArrayBuffer; mimeType: string }) => {
				const ext = fd.name.includes(".") ? `.${fd.name.split(".").pop()?.toLowerCase()}` : "";
				return ext !== "" && supportedExtensionsSet.has(ext);
			}
		);

		if (filtered.length === 0) {
			toast.error(t("no_supported_files_in_folder"));
			return;
		}

		const newFiles: FileWithId[] = filtered.map(
			(fd: { name: string; data: ArrayBuffer; mimeType: string }) => ({
				id: crypto.randomUUID?.() ?? `file-${Date.now()}-${Math.random().toString(36)}`,
				file: new File([fd.data], fd.name, { type: fd.mimeType }),
			})
		);
		setFolderUpload(null);
		setFiles((prev) => [...prev, ...newFiles]);
	}, [electronAPI, supportedExtensionsSet, t]);

	const handleFolderChange = useCallback(
		(e: ChangeEvent<HTMLInputElement>) => {
			const fileList = e.target.files;
			if (!fileList || fileList.length === 0) return;

			const allFiles = Array.from(fileList);
			const firstPath = allFiles[0]?.webkitRelativePath || "";
			const folderName = firstPath.split("/")[0];

			if (!folderName) {
				addFiles(allFiles);
				e.target.value = "";
				return;
			}

			const entries: FolderEntry[] = allFiles
				.filter((f) => {
					const ext = f.name.includes(".") ? `.${f.name.split(".").pop()?.toLowerCase()}` : "";
					return ext !== "" && supportedExtensionsSet.has(ext);
				})
				.map((f) => ({
					id: crypto.randomUUID?.() ?? `file-${Date.now()}-${Math.random().toString(36)}`,
					file: f,
					relativePath: f.webkitRelativePath.substring(folderName.length + 1),
				}));

			if (entries.length === 0) {
				toast.error(t("no_supported_files_in_folder"));
				e.target.value = "";
				return;
			}

			setFiles([]);
			setFolderUpload({ folderName, entries });
			e.target.value = "";
		},
		[addFiles, supportedExtensionsSet, t]
	);

	const formatFileSize = (bytes: number) => {
		if (bytes === 0) return "0 Bytes";
		const k = 1024;
		const sizes = ["Bytes", "KB", "MB", "GB", "TB"];
		const i = Math.floor(Math.log(bytes) / Math.log(k));
		return `${parseFloat((bytes / k ** i).toFixed(2))} ${sizes[i]}`;
	};

	const totalFileSize = folderUpload
		? folderUpload.entries.reduce((total, entry) => total + entry.file.size, 0)
		: files.reduce((total, entry) => total + entry.file.size, 0);

	const fileCount = folderUpload ? folderUpload.entries.length : files.length;
	const hasContent = files.length > 0 || folderUpload !== null;
	const isAnyUploading = isUploading || isFolderUploading;

	const folderTreeItems = useMemo(() => {
		if (!folderUpload) return [];
		return flattenTree(buildFolderTree(folderUpload.entries));
	}, [folderUpload]);

	const handleAccordionChange = useCallback(
		(value: string) => {
			setAccordionValue(value);
			onAccordionStateChange?.(value === "supported-file-types");
		},
		[onAccordionStateChange]
	);

	const handleFolderUpload = async () => {
		if (!folderUpload) return;

		setUploadProgress(0);
		setIsFolderUploading(true);
		const total = folderUpload.entries.length;
		trackDocumentUploadStarted(Number(workspaceId), total, totalFileSize);

		try {
			const batches: FolderEntry[][] = [];
			let currentBatch: FolderEntry[] = [];
			let currentSize = 0;

			for (const entry of folderUpload.entries) {
				const size = entry.file.size;

				if (size >= FOLDER_BATCH_SIZE_BYTES) {
					if (currentBatch.length > 0) {
						batches.push(currentBatch);
						currentBatch = [];
						currentSize = 0;
					}
					batches.push([entry]);
					continue;
				}

				if (
					currentBatch.length >= FOLDER_BATCH_MAX_FILES ||
					currentSize + size > FOLDER_BATCH_SIZE_BYTES
				) {
					batches.push(currentBatch);
					currentBatch = [];
					currentSize = 0;
				}

				currentBatch.push(entry);
				currentSize += size;
			}

			if (currentBatch.length > 0) {
				batches.push(currentBatch);
			}

			let rootFolderId: number | null = null;
			let uploaded = 0;

			for (const batch of batches) {
				const result = await documentsApiService.folderUploadFiles(
					batch.map((e) => e.file),
					{
						folder_name: folderUpload.folderName,
						workspace_id: Number(workspaceId),
						relative_paths: batch.map((e) => e.relativePath),
						root_folder_id: rootFolderId,
						use_vision_llm: useVisionLlm,
						parser: activeParser,
					}
				);

				if (result.root_folder_id && !rootFolderId) {
					rootFolderId = result.root_folder_id;
				}

				uploaded += batch.length;
				setUploadProgress(Math.round((uploaded / total) * 100));
			}

			// Ingestion outcome is now emitted server-side
			// (document_processing_completed/_failed in document_tasks.py); the
			// upload POST succeeding only means the file was accepted, not processed.
			toast(t("upload_initiated"), { description: t("upload_initiated_desc") });
			setFolderUpload(null);
			onSuccess?.({ documentIds: [], folderId: rootFolderId });
		} catch (error) {
			const message = error instanceof Error ? error.message : "Upload failed";
			toast(t("upload_error"), {
				description: `${t("upload_error_desc")}: ${message}`,
			});
		} finally {
			setIsFolderUploading(false);
			setUploadProgress(0);
		}
	};

	const handleUpload = async () => {
		if (folderUpload) {
			await handleFolderUpload();
			return;
		}

		setUploadProgress(0);
		trackDocumentUploadStarted(Number(workspaceId), files.length, totalFileSize);

		progressIntervalRef.current = setInterval(() => {
			setUploadProgress((prev) => (prev >= 90 ? prev : prev + Math.random() * 10));
		}, 200);

		const rawFiles = files.map((entry) => entry.file);
		uploadDocuments(
			{
				files: rawFiles,
				workspace_id: Number(workspaceId),
				use_vision_llm: useVisionLlm,
				parser: activeParser,
			},
			{
				onSuccess: (result) => {
					if (progressIntervalRef.current) clearInterval(progressIntervalRef.current);
					setUploadProgress(100);
					// Ingestion outcome now server-side (document_processing_*).
					toast(t("upload_initiated"), { description: t("upload_initiated_desc") });
					onSuccess?.({
						documentIds: [
							...(result?.document_ids ?? []),
							...(result?.duplicate_document_ids ?? []),
						],
						folderId: null,
					});
				},
				onError: (error: unknown) => {
					if (progressIntervalRef.current) clearInterval(progressIntervalRef.current);
					setUploadProgress(0);
					const message = error instanceof Error ? error.message : "Upload failed";
					toast(t("upload_error"), {
						description: `${t("upload_error_desc")}: ${message}`,
					});
				},
			}
		);
	};

	const renderBrowseButton = (options?: { compact?: boolean; fullWidth?: boolean }) => {
		const { compact, fullWidth } = options ?? {};
		const sizeClass = compact ? "h-7" : "h-8";
		const widthClass = fullWidth ? "w-full" : "";

		if (isElectron) {
			return (
				<DropdownMenu>
					<DropdownMenuTrigger asChild onClick={(e) => e.stopPropagation()}>
						<Button
							variant="ghost"
							size="sm"
							className={`text-xs gap-1 bg-neutral-700/50 hover:bg-accent hover:text-accent-foreground ${sizeClass} ${widthClass}`}
						>
							Browse
							<ChevronDown className="h-3 w-3 opacity-60" />
						</Button>
					</DropdownMenuTrigger>
					<DropdownMenuContent
						align="center"
						className="dark:bg-neutral-800"
						onClick={(e) => e.stopPropagation()}
					>
						<DropdownMenuItem onClick={handleBrowseFiles}>
							<FileIcon className="h-4 w-4 mr-2" />
							Files
						</DropdownMenuItem>
						<DropdownMenuItem onClick={() => folderInputRef.current?.click()}>
							<FolderOpen className="h-4 w-4 mr-2" />
							Folder
						</DropdownMenuItem>
					</DropdownMenuContent>
				</DropdownMenu>
			);
		}

		return (
			<DropdownMenu>
				<DropdownMenuTrigger asChild onClick={(e) => e.stopPropagation()}>
					<Button
						variant="ghost"
						size="sm"
						className={`text-xs gap-1 bg-neutral-700/50 hover:bg-accent hover:text-accent-foreground ${sizeClass} ${widthClass}`}
					>
						Browse
						<ChevronDown className="h-3 w-3 opacity-60" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent
					align="center"
					className="dark:bg-neutral-800"
					onClick={(e) => e.stopPropagation()}
				>
					<DropdownMenuItem onClick={() => fileInputRef.current?.click()}>
						<FileIcon className="h-4 w-4 mr-2" />
						{t("browse_files")}
					</DropdownMenuItem>
					<DropdownMenuItem onClick={() => folderInputRef.current?.click()}>
						<FolderOpen className="h-4 w-4 mr-2" />
						{t("browse_folder")}
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
		);
	};

	return (
		<div className="space-y-2 w-full mx-auto">
			{/* Hidden file input */}
			<input
				{...getInputProps({ accept: supportedExtensions.join(",") })}
				ref={fileInputRef}
				className="hidden"
				onClick={handleFileInputClick}
			/>

			{/* Hidden folder input for web folder browsing */}
			<input
				ref={folderInputRef}
				type="file"
				className="hidden"
				onChange={handleFolderChange}
				multiple
				{...({ webkitdirectory: "", directory: "" } as React.InputHTMLAttributes<HTMLInputElement>)}
			/>

			{/* MOBILE DROP ZONE */}
			<div className="sm:hidden">
				{hasContent ? (
					isElectron ? (
						<div className="flex w-full justify-center">
							{renderBrowseButton({ compact: true })}
						</div>
					) : (
						<Button
							type="button"
							variant="ghost"
							className="h-8 w-full gap-1.5 rounded-md border border-dashed border-muted-foreground/30 px-0 text-xs text-muted-foreground transition-colors hover:border-foreground/50 hover:bg-transparent hover:text-accent-foreground"
							onClick={() => fileInputRef.current?.click()}
						>
							Add more files
						</Button>
					)
				) : (
					<div className="flex w-full flex-col items-center gap-4 bg-transparent px-4 py-12 select-none">
						{isElectron ? (
							<div className="flex w-full flex-col items-center gap-4">
								<div className="text-center space-y-1.5">
									<p className="text-base font-medium">{t("select_files_or_folder")}</p>
									<p className="text-sm text-muted-foreground">{t("file_size_limit")}</p>
								</div>
							</div>
						) : (
							<Button
								type="button"
								variant="ghost"
								className="h-auto w-full flex-col gap-4 whitespace-normal bg-transparent p-0 text-foreground hover:bg-transparent hover:text-foreground"
								onClick={() => fileInputRef.current?.click()}
							>
								<div className="text-center space-y-1.5">
									<p className="text-base font-medium">{t("tap_select_files_or_folder")}</p>
									<p className="text-sm text-muted-foreground">{t("file_size_limit")}</p>
								</div>
							</Button>
						)}
						<fieldset
							className="mt-1 flex w-full justify-center border-none p-0 m-0"
							onClick={(e) => e.stopPropagation()}
							onKeyDown={(e) => e.stopPropagation()}
						>
							{renderBrowseButton()}
						</fieldset>
					</div>
				)}
			</div>

			{/* DESKTOP DROP ZONE */}
			<div
				{...getRootProps()}
				className={`hidden sm:block border-2 border-dashed rounded-lg transition-colors border-muted-foreground/30 hover:border-foreground/70 cursor-pointer ${hasContent ? "p-3" : "py-20 px-4"}`}
			>
				{hasContent ? (
					<div className="flex items-center gap-3">
						<span className="text-xs text-muted-foreground flex-1 truncate">
							{isDragActive ? t("drop_files") : t("drag_drop_more")}
						</span>
						{renderBrowseButton({ compact: true })}
					</div>
				) : (
					<div className="relative">
						{isDragActive && (
							<div className="absolute inset-0 flex flex-col items-center justify-center gap-2">
								<p className="text-sm font-medium text-primary">{t("drop_files")}</p>
							</div>
						)}
						<div className={`flex flex-col items-center gap-2 ${isDragActive ? "invisible" : ""}`}>
							<p className="text-sm font-medium">{t("drag_drop")}</p>
							<p className="text-xs text-muted-foreground">{t("file_size_limit")}</p>
							<div className="mt-1">{renderBrowseButton()}</div>
						</div>
					</div>
				)}
			</div>

			{rejectedNotice && (
				<output className="flex items-start gap-2 rounded-lg border border-amber-500/40 bg-amber-500/5 p-3 text-xs">
					<div className="min-w-0 flex-1 space-y-0.5">
						<p className="font-medium text-sm">
							{t("unsupported_files_title", { count: rejectedNotice.names.length })}
						</p>
						<p className="text-muted-foreground break-words">
							{t("unsupported_files_desc", {
								parser: parserName(rejectedNotice.parser),
								files: rejectedNotice.names.join(", "),
							})}
						</p>
					</div>
					<Button
						type="button"
						variant="ghost"
						size="icon"
						className="h-6 w-6 shrink-0"
						aria-label={t("unsupported_files_dismiss")}
						onClick={() => setRejectedNotice(null)}
					>
						<X className="h-3 w-3" />
					</Button>
				</output>
			)}

			{/* FILES SELECTED */}
			{hasContent && (
				<div className="rounded-lg border border-border p-3 space-y-2">
					<div className="flex items-center justify-between">
						<p className="text-sm font-medium">
							{folderUpload ? (
								<>
									<FolderOpen className="inline h-4 w-4 mr-1 -mt-0.5" />
									{folderUpload.folderName}
									<Dot className="inline h-4 w-4" />
									{folderUpload.entries.length}{" "}
									{folderUpload.entries.length === 1 ? "file" : "files"}
									<Dot className="inline h-4 w-4" />
									{formatFileSize(totalFileSize)}
								</>
							) : (
								<>
									{t("selected_files", { count: files.length })}
									<Dot className="inline h-4 w-4" />
									{formatFileSize(totalFileSize)}
								</>
							)}
						</p>
						<Button
							variant="ghost"
							size="sm"
							className="h-7 text-xs text-muted-foreground hover:text-accent-foreground"
							onClick={() => {
								setFiles([]);
								setFolderUpload(null);
							}}
							disabled={isAnyUploading}
						>
							{t("clear_all")}
						</Button>
					</div>

					<div className="max-h-[160px] sm:max-h-[200px] overflow-y-auto -mx-1">
						{folderUpload
							? folderTreeItems.map((item) => (
									<div
										key={item.path}
										className="flex items-center gap-1.5 py-0.5 px-2"
										style={{ paddingLeft: `${item.depth * 16 + 8}px` }}
									>
										{item.isFolder ? (
											<FolderOpen className="h-3.5 w-3.5 text-blue-400 shrink-0" />
										) : (
											<FileIcon className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
										)}
										<span className="text-sm truncate flex-1 min-w-0">{item.name}</span>
										{!item.isFolder && item.size != null && (
											<span className="text-xs text-muted-foreground shrink-0">
												{formatFileSize(item.size)}
											</span>
										)}
									</div>
								))
							: files.map((entry) => (
									<div
										key={entry.id}
										className="flex items-center gap-2 py-1.5 px-2 rounded-md hover:bg-accent hover:text-accent-foreground group"
									>
										<span className="text-[10px] font-medium uppercase leading-none bg-muted px-1.5 py-0.5 rounded text-muted-foreground shrink-0">
											{entry.file.name.split(".").pop() || "?"}
										</span>
										<span className="text-sm truncate flex-1 min-w-0">{entry.file.name}</span>
										<span className="text-xs text-muted-foreground shrink-0">
											{formatFileSize(entry.file.size)}
										</span>
										<Button
											variant="ghost"
											size="icon"
											className="h-6 w-6 shrink-0"
											onClick={() => setFiles((prev) => prev.filter((e) => e.id !== entry.id))}
											disabled={isAnyUploading}
										>
											<X className="h-3 w-3" />
										</Button>
									</div>
								))}
					</div>

					{isAnyUploading && (
						<div className="space-y-1">
							<div className="flex items-center justify-between text-xs">
								<span>{folderUpload ? t("uploading_folder") : t("uploading_files")}</span>
								<span>{Math.round(uploadProgress)}%</span>
							</div>
							<Progress value={uploadProgress} className="h-1.5" />
						</div>
					)}

					<div className={toggleRowClass}>
						<div className="space-y-0.5">
							<p className="font-medium text-sm">{t("vision_llm_label")}</p>
							<p className="text-xs text-muted-foreground">{t("vision_llm_desc")}</p>
						</div>
						<Switch checked={useVisionLlm} onCheckedChange={handleVisionChange} />
					</div>

					{parserChoice.options.length > 0 && (
						<div className="space-y-1.5">
							<p className="font-medium text-sm px-1">{t("parser_label")}</p>
							<div className="grid grid-cols-2 gap-2">
								{parserChoice.options.map((option) => {
									const selected = activeParser === option.parser;
									const Icon = option.parser === "cloud" ? Cloud : HardDrive;
									return (
										<Button
											key={option.parser}
											type="button"
											onClick={() => handleParserChange(option.parser)}
											disabled={!option.available || isAnyUploading}
											aria-pressed={selected}
											variant="ghost"
											className={`h-auto w-full items-start justify-start whitespace-normal rounded-lg border p-3 text-left transition-colors hover:bg-transparent hover:text-foreground disabled:opacity-60 ${
												selected
													? "border-primary bg-primary/5"
													: "border-border hover:border-muted-foreground/50"
											}`}
										>
											<Icon
												className={`h-4 w-4 mt-0.5 shrink-0 ${selected ? "text-primary" : "text-muted-foreground"}`}
											/>
											<div className="space-y-0.5 min-w-0">
												<p className="font-medium text-sm">
													{option.parser === "cloud" ? t("parser_cloud") : t("parser_local")}
												</p>
												<p className="text-xs text-muted-foreground">
													{option.available
														? option.parser === "cloud"
															? t("parser_cloud_desc")
															: t("parser_local_desc")
														: t(unavailableReasonKey(option.unavailableReason))}
													{option.available && option.costMultiplier !== null && (
														<> {t("parser_cost_note", { multiplier: option.costMultiplier })}</>
													)}
													{option.available ? <ParserCreditHint parser={option.parser} /> : null}
												</p>
											</div>
										</Button>
									);
								})}
							</div>
						</div>
					)}

					<Button
						className="w-full relative"
						onClick={handleUpload}
						disabled={isAnyUploading || fileCount === 0}
					>
						<span className={isAnyUploading ? "opacity-0" : ""}>
							{folderUpload
								? t("upload_folder_button", { count: fileCount })
								: t("upload_button", { count: fileCount })}
						</span>
						{isAnyUploading && <Spinner size="sm" className="absolute" />}
					</Button>
				</div>
			)}

			{/* SUPPORTED FORMATS */}
			<Accordion
				type="single"
				collapsible
				value={accordionValue}
				onValueChange={handleAccordionChange}
				className="w-full mt-5"
			>
				<AccordionItem value="supported-file-types" className="border border-border rounded-lg">
					<AccordionTrigger className="px-3 py-2.5 hover:no-underline !items-center [&>svg]:!translate-y-0">
						<span className="text-xs sm:text-sm text-muted-foreground font-normal">
							{t("supported_file_types")}
						</span>
					</AccordionTrigger>
					<AccordionContent className="px-3 pb-3">
						<div className="flex flex-wrap gap-1.5">
							{supportedExtensions.map((ext) => (
								<Badge
									key={ext}
									variant="secondary"
									className="rounded border-0 bg-neutral-200/80 dark:bg-neutral-700/60 text-muted-foreground text-[10px] px-2 py-0.5 font-normal"
								>
									{ext}
								</Badge>
							))}
						</div>
					</AccordionContent>
				</AccordionItem>
			</Accordion>
		</div>
	);
}
