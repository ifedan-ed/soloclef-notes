"use client";

import { useQuery } from "@tanstack/react-query";
import { List, Sparkles, X } from "lucide-react";
import { useTranslations } from "next-intl";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { artifactDownloadPath } from "@/features/artifacts/api/artifact-download-path";
import { artifactManifestQueryOptions } from "@/features/artifacts/api/artifact-queries";
import {
	artifactDownloadFilename,
	selectPrimaryArtifactFile,
} from "@/features/artifacts/lib/artifact-selectors";
import type { ArtifactManifest } from "@/features/artifacts/model/artifact";
import { ArtifactRenderer } from "@/features/artifacts/rendering/artifact-renderer";
import { ArtifactDownloadButton } from "@/features/artifacts/ui/artifact-download-button";
import { cn } from "@/lib/utils";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import { useStudioOutputs } from "../../hooks/use-notebook-queries";
import { useReportView } from "../../hooks/use-report-view";
import { reportPassageMessage } from "../../lib/explain-messages";
import { READING_LINE_PX } from "../../lib/report-bridge";
import { activeSectionIndex, isReadable, splitReport } from "../../lib/report-outline";
import { placeSuggestions } from "../../lib/report-suggestions";
import type { ReportSuggestion, StudioOutput } from "../../model/notebook";
import { useExplain } from "../../state/explain";
import { ReportDocument } from "./report-document";
import { type ReportFrameHandle, ReportHtmlFrame } from "./report-html-frame";
import { ReportToc, type TocItem } from "./report-toc";

// Scroll events during a jump's smooth scroll must not move the highlight.
const JUMP_LOCK_MS = 900;
const MIN_PASSAGE_CHARS = 3;
/** What keeps its place on screen when the report changes width. */
const ANCHOR_BLOCKS =
	"[data-report-suggestion], [data-report-section] :is(h1, h2, h3, h4, h5, h6, p, li, pre, table, blockquote)";

function prefersReducedMotion(): boolean {
	return (
		typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches
	);
}

type Selected = { text: string; top: number; left: number };

/**
 * A report's reading view inside the Studio output view (ui/studio/
 * output-view), modelled on NotebookLM's: a numbered contents list that
 * follows the reading position, suggestions for Studio outputs under the
 * sections they deepen (added ones are made and shown in place), and an
 * "Explain" for a selected passage. Markdown reports are read as text; an
 * interactive report keeps its own page and gets the contents through a
 * postMessage bridge; a report with no readable text shows its file.
 *
 * ``compact`` (the Studio column, or a phone) turns the contents list into a
 * button and a sheet. When the view changes size the reader stays on the
 * section they were reading.
 *
 * ``shared`` is a public link's copy (features/public-sharing): the same
 * reading view and contents from the manifest the link was sent, read-only,
 * with no suggestions and no Explain (there is no notebook chat to ask).
 */
export function ReportBody({
	workspaceId,
	notebookId,
	output,
	compact,
	controls,
	shared,
}: {
	workspaceId: number;
	notebookId: number;
	output: Pick<StudioOutput, "id" | "title" | "artifact_id" | "options">;
	compact: boolean;
	/** The output view's header slot for the contents toggle and download. */
	controls: HTMLElement | null;
	shared?: { manifest: ArtifactManifest };
}) {
	const t = useTranslations("report_view");
	const tExplain = useTranslations("explain");
	const explain = useExplain();
	const artifactId = output.artifact_id ?? 0;
	const manifest = useQuery({
		...artifactManifestQueryOptions(workspaceId, artifactId),
		enabled: artifactId > 0 && !shared,
	});
	const manifestData = shared?.manifest ?? manifest.data;
	const { view, add, dismiss } = useReportView(
		workspaceId,
		notebookId,
		output.id,
		artifactId > 0 && !shared
	);
	const outputs = useStudioOutputs(workspaceId, notebookId);
	const showSpinner = useDelayedFlag(!manifestData && manifest.isPending);

	const outline = useMemo(
		() => splitReport(manifestData?.markdown_representation ?? ""),
		[manifestData?.markdown_representation]
	);
	const primary = manifestData ? selectPrimaryArtifactFile(manifestData) : undefined;
	const interactive =
		!!primary &&
		(view.data?.format === "interactive" ||
			output.options.format === "interactive" ||
			primary.mime_type.startsWith("text/html"));
	const mode: "loading" | "error" | "interactive" | "document" | "file" = manifestData
		? interactive
			? "interactive"
			: isReadable(outline)
				? "document"
				: "file"
		: manifest.isError
			? "error"
			: "loading";

	const [frameToc, setFrameToc] = useState<string[]>([]);
	const tocItems: TocItem[] =
		mode === "document"
			? outline.sections.map((s) => ({ index: s.index, label: s.heading }))
			: mode === "interactive"
				? frameToc.map((label, index) => ({ index, label }))
				: [];
	const [active, setActive] = useState(0);
	const activeRef = useRef(0);
	activeRef.current = active;
	const [tocCollapsed, setTocCollapsed] = useState(false);
	const [tocSheet, setTocSheet] = useState(false);
	const scroller = useRef<HTMLDivElement>(null);
	const article = useRef<HTMLDivElement>(null);
	const frame = useRef<ReportFrameHandle>(null);
	const lockedUntil = useRef(0);
	const [selected, setSelected] = useState<Selected | null>(null);

	// Scroll-spy for the Markdown document.
	useEffect(() => {
		const element = scroller.current;
		if (mode !== "document" || !element) return;
		let pending = 0;
		const measure = () => {
			pending = 0;
			if (Date.now() < lockedUntil.current) return;
			const top = element.getBoundingClientRect().top;
			const tops = Array.from(
				element.querySelectorAll<HTMLElement>("[data-report-section]"),
				(section) => section.getBoundingClientRect().top - top
			);
			const atBottom =
				element.scrollHeight > element.clientHeight &&
				element.scrollTop + element.clientHeight >= element.scrollHeight - 4;
			const index = activeSectionIndex(tops, { offset: READING_LINE_PX, atBottom });
			if (index >= 0) setActive(index);
		};
		const onScroll = () => {
			if (!pending) pending = window.requestAnimationFrame(measure);
		};
		measure();
		element.addEventListener("scroll", onScroll, { passive: true });
		return () => {
			element.removeEventListener("scroll", onScroll);
			if (pending) window.cancelAnimationFrame(pending);
		};
	}, [mode]);

	const scrollToSection = useCallback((index: number, smooth: boolean) => {
		const element = scroller.current;
		const section = element?.querySelector<HTMLElement>(`[data-report-section="${index}"]`);
		if (!element || !section) return false;
		const top =
			section.getBoundingClientRect().top -
			element.getBoundingClientRect().top +
			element.scrollTop -
			16;
		element.scrollTo({ top, behavior: smooth ? "smooth" : "auto" });
		return true;
	}, []);

	const jump = useCallback(
		(index: number) => {
			setActive(index);
			setTocSheet(false);
			lockedUntil.current = Date.now() + JUMP_LOCK_MS;
			if (mode === "interactive") {
				frame.current?.goto(index);
				return;
			}
			if (scrollToSection(index, !prefersReducedMotion())) {
				scroller.current
					?.querySelector<HTMLElement>(`[data-report-section="${index}"]`)
					?.focus({ preventScroll: true });
			}
		},
		[mode, scrollToSection]
	);

	// Text re-wraps when the view changes width (column ↔ window). Keep the
	// reader's place: the block they last touched (an embedded quiz whose
	// Explain they pressed) or else the first block in view stays where it
	// was on screen. Hidden (a phone's Chat tab) the scroller loses its
	// offset; put it back.
	useEffect(() => {
		const element = scroller.current;
		if (mode !== "document" || !element) return;
		let width = element.clientWidth;
		let saved = element.scrollTop;
		let anchor: { node: HTMLElement; offset: number } | null = null;
		const offsetOf = (node: HTMLElement) =>
			node.getBoundingClientRect().top - element.getBoundingClientRect().top;
		const firstInView = () => {
			const top = element.getBoundingClientRect().top;
			for (const node of element.querySelectorAll<HTMLElement>(ANCHOR_BLOCKS)) {
				if (node.getBoundingClientRect().bottom > top + 1) return node;
			}
			return null;
		};
		let pending = 0;
		const onScroll = () => {
			if (element.clientHeight === 0) return;
			saved = element.scrollTop;
			if (pending) return;
			pending = window.requestAnimationFrame(() => {
				pending = 0;
				const node = firstInView();
				anchor = node ? { node, offset: offsetOf(node) } : null;
			});
		};
		const onPointer = (event: PointerEvent) => {
			const node = (event.target as Element | null)?.closest<HTMLElement>(ANCHOR_BLOCKS);
			if (node && element.contains(node)) anchor = { node, offset: offsetOf(node) };
		};
		const observer = new ResizeObserver(() => {
			const next = element.clientWidth;
			if (next === width) return;
			const wasHidden = width === 0;
			width = next;
			if (next === 0) return;
			lockedUntil.current = Date.now() + 200;
			setSelected(null);
			if (wasHidden) element.scrollTop = saved;
			else if (anchor?.node.isConnected) {
				element.scrollTop += offsetOf(anchor.node) - anchor.offset;
			}
		});
		element.addEventListener("scroll", onScroll, { passive: true });
		element.addEventListener("pointerdown", onPointer, { capture: true, passive: true });
		observer.observe(element);
		return () => {
			element.removeEventListener("scroll", onScroll);
			element.removeEventListener("pointerdown", onPointer, { capture: true });
			observer.disconnect();
			if (pending) window.cancelAnimationFrame(pending);
		};
	}, [mode]);

	// "Explain" for a passage selected in the document.
	useEffect(() => {
		if (mode !== "document" || !explain) return;
		const update = () => {
			const container = article.current;
			const selection = window.getSelection();
			if (!container || !selection || selection.isCollapsed || selection.rangeCount === 0) {
				setSelected(null);
				return;
			}
			const range = selection.getRangeAt(0);
			const text = selection.toString().trim();
			if (text.length < MIN_PASSAGE_CHARS || !container.contains(range.commonAncestorContainer)) {
				setSelected(null);
				return;
			}
			const box = range.getBoundingClientRect();
			const origin = container.getBoundingClientRect();
			setSelected({
				text,
				top: box.bottom - origin.top + 8,
				left: Math.min(
					Math.max(box.left + box.width / 2 - origin.left, 56),
					Math.max(origin.width - 56, 56)
				),
			});
		};
		// A selection made before the chat was ready still gets its button.
		update();
		document.addEventListener("selectionchange", update);
		return () => document.removeEventListener("selectionchange", update);
	}, [mode, explain]);

	const explainSelection = () => {
		if (!selected || !explain) return;
		const message = reportPassageMessage(tExplain, {
			title: outline.title ?? output.title,
			passage: selected.text,
		});
		setSelected(null);
		window.getSelection()?.removeAllRanges();
		explain(message);
	};

	const toggleToc = () => {
		if (compact) setTocSheet((open) => !open);
		else setTocCollapsed((collapsed) => !collapsed);
	};
	// Leaving the column for the window closes the sheet.
	useEffect(() => {
		if (!compact) setTocSheet(false);
	}, [compact]);

	const outputsById = useMemo(
		() => new Map((outputs.data ?? []).map((item) => [item.id, item])),
		[outputs.data]
	);
	const placed = useMemo(
		() => placeSuggestions(outline.sections, view.data?.suggestions ?? []),
		[outline.sections, view.data?.suggestions]
	);
	const [failedToStart, setFailedToStart] = useState<ReadonlySet<string>>(new Set());
	const addSuggestion = (suggestion: ReportSuggestion) => {
		setFailedToStart((previous) => {
			const next = new Set(previous);
			next.delete(suggestion.id);
			return next;
		});
		add.mutate(suggestion.id, {
			onError: () => setFailedToStart((previous) => new Set(previous).add(suggestion.id)),
		});
	};

	const hasToc = tocItems.length > 0;
	const tocVisible = compact ? tocSheet : !tocCollapsed;

	const headerControls = (
		<>
			{hasToc ? (
				<Button
					variant="ghost"
					size="icon"
					className="size-8 shrink-0 rounded-full text-muted-foreground"
					onClick={toggleToc}
					aria-label={t("toggle_contents")}
					aria-expanded={tocVisible}
				>
					<List className="size-4" />
				</Button>
			) : null}
			{manifestData && primary && mode !== "interactive" ? (
				<ArtifactDownloadButton
					path={shared ? primary.content_url : artifactDownloadPath(workspaceId, artifactId)}
					filename={artifactDownloadFilename(manifestData, artifactId)}
					className="size-8 shrink-0 rounded-full text-muted-foreground"
				/>
			) : null}
		</>
	);

	return (
		<div className="relative flex h-full min-h-0 flex-1">
			{controls ? createPortal(headerControls, controls) : null}
			{hasToc && !compact && !tocCollapsed ? (
				<aside className="w-64 shrink-0 overflow-y-auto border-r p-3 xl:w-72">
					<ReportToc items={tocItems} active={active} onJump={jump} />
				</aside>
			) : null}

			<div
				ref={scroller}
				className={cn(
					"relative min-h-0 min-w-0 flex-1",
					mode === "document" ? "overflow-y-auto overscroll-contain" : "overflow-hidden"
				)}
			>
				{mode === "document" ? (
					<div ref={article} className="relative">
						<ReportDocument
							outline={outline}
							fallbackTitle={output.title}
							placed={placed}
							outputs={outputsById}
							workspaceId={workspaceId}
							adding={add.isPending ? (add.variables ?? null) : null}
							failedToStart={failedToStart}
							onAdd={addSuggestion}
							onDismiss={(suggestion) => dismiss.mutate(suggestion.id)}
						/>
						{selected ? (
							<Button
								size="sm"
								className="absolute z-10 -translate-x-1/2 rounded-full shadow-lg"
								style={{ top: selected.top, left: selected.left }}
								// Act on press: a tap (or mousedown) outside the text collapses the
								// selection, which would take this button away before its click.
								onPointerDown={(event) => {
									if (event.button !== 0) return;
									event.preventDefault();
									explainSelection();
								}}
								onMouseDown={(event) => event.preventDefault()}
								// Keyboard (Enter / Space).
								onClick={explainSelection}
								aria-label={tExplain("selection_label")}
							>
								<Sparkles className="size-4" />
								{tExplain("button")}
							</Button>
						) : null}
					</div>
				) : mode === "interactive" && primary ? (
					<ReportHtmlFrame
						ref={frame}
						primary={primary}
						onToc={setFrameToc}
						onActive={(index) => {
							if (Date.now() >= lockedUntil.current) setActive(index);
						}}
					/>
				) : mode === "file" && manifestData ? (
					<ArtifactRenderer
						workspaceId={workspaceId}
						manifest={manifestData}
						zoomControlsContainer={null}
					/>
				) : mode === "error" ? (
					<div
						role="alert"
						className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center"
					>
						<p className="text-sm text-muted-foreground">{t("open_failed")}</p>
						<Button variant="secondary" size="sm" onClick={() => void manifest.refetch()}>
							{t("try_again")}
						</Button>
					</div>
				) : (
					<div aria-busy="true" className="flex h-full items-center justify-center">
						{showSpinner ? <Spinner size="lg" /> : null}
						<span className="sr-only">{t("loading")}</span>
					</div>
				)}
			</div>

			{compact && tocSheet ? (
				<>
					<button
						type="button"
						aria-label={t("close_panel")}
						className="absolute inset-0 z-10 bg-black/30"
						onClick={() => setTocSheet(false)}
					/>
					<div
						role="dialog"
						aria-label={t("contents")}
						className="absolute inset-x-0 bottom-0 z-20 max-h-[75%] overflow-y-auto rounded-t-2xl border-t bg-background p-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] shadow-2xl"
					>
						<div className="flex items-center justify-between gap-2 px-3 pb-2">
							<p className="text-sm font-medium">{t("contents")}</p>
							<Button
								variant="ghost"
								size="icon"
								className="size-8 rounded-full"
								onClick={() => setTocSheet(false)}
								aria-label={t("close_panel")}
							>
								<X className="size-4" />
							</Button>
						</div>
						<ReportToc items={tocItems} active={active} onJump={jump} />
					</div>
				</>
			) : null}
		</div>
	);
}
