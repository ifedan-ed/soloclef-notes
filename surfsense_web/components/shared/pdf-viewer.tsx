"use client";

import { ZoomInIcon, ZoomOutIcon } from "lucide-react";
import type { PDFDocumentLoadingTask, PDFDocumentProxy } from "pdfjs-dist";
import * as pdfjsLib from "pdfjs-dist";
import type { TextItem } from "pdfjs-dist/types/src/display/api";
import type { PDFViewer as PDFViewerCore } from "pdfjs-dist/web/pdf_viewer.mjs";
import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import {
	citedRects,
	findTextOnPage,
	type PercentRect,
	pageToOpen,
} from "@/features/file-viewers/pdf-cited-text";
import { authenticatedFetch, getAuthHeaders } from "@/lib/auth-fetch";

pdfjsLib.GlobalWorkerOptions.workerSrc = new URL(
	"pdfjs-dist/build/pdf.worker.min.mjs",
	import.meta.url
).toString();

interface PdfViewerProps {
	pdfUrl: string;
	isPublic?: boolean;
	/** Extra actions rendered on the right side of the zoom toolbar (e.g. download, version switcher) */
	toolbarActions?: ReactNode;
	/** Optional panel-header target for zoom controls. Passing null reserves it while it mounts. */
	zoomControlsContainer?: HTMLElement | null;
	/**
	 * Fetch only the byte ranges of the pages on screen when the server serves
	 * ranges, so a very large PDF shows its first page at once instead of after
	 * a full download. Falls back to loading the whole file when it does not.
	 */
	rangeRequests?: boolean;
	/** 1-based page to open at, once the document is laid out. */
	initialPage?: number;
	/**
	 * Text to find on ``initialPage`` (a cited chunk): the view scrolls to it
	 * and marks it. Only that page's text is read, so with range requests
	 * nothing else is fetched for it.
	 */
	highlightText?: string;
}

// One range request's size: a few pages of a typical PDF.
const RANGE_CHUNK_BYTES = 256 * 1024;

type EmbeddedPdfViewer = Omit<PDFViewerCore, "setDocument"> & {
	setDocument(pdfDocument: PDFDocumentProxy | null): void;
};

type PDFViewerOptionsWithSignal = ConstructorParameters<typeof PDFViewerCore>[0] & {
	abortSignal: AbortSignal;
};

interface TouchManagerInstance {
	destroy(): void;
}

interface TouchManagerConstructor {
	new (options: {
		container: HTMLElement;
		isPinchingDisabled?: () => boolean;
		onPinchStart?: () => void;
		onPinching?: (origin: number[], previousDistance: number, distance: number) => void;
		onPinchEnd?: () => void;
		signal: AbortSignal;
	}): TouchManagerInstance;
}

interface PageRenderedEvent {
	isDetailView: boolean;
	pageNumber: number;
	source?: { div?: HTMLElement };
}

interface CitedMark {
	pageNumber: number;
	rects: PercentRect[];
}

const CITED_MARK_CLASS = "surfsense-pdf-cited";

function drawCitedMark(pageDiv: HTMLElement, rects: PercentRect[]) {
	pageDiv.querySelector(`:scope > .${CITED_MARK_CLASS}`)?.remove();
	const layer = document.createElement("div");
	layer.className = CITED_MARK_CLASS;
	layer.setAttribute("aria-hidden", "true");
	for (const rect of rects) {
		const mark = document.createElement("span");
		mark.style.left = `${rect.left}%`;
		mark.style.top = `${rect.top}%`;
		mark.style.width = `${rect.width}%`;
		mark.style.height = `${rect.height}%`;
		layer.append(mark);
	}
	pageDiv.append(layer);
}

function clearCitedMarks(viewerElement: HTMLElement) {
	for (const layer of viewerElement.querySelectorAll(`.${CITED_MARK_CLASS}`)) layer.remove();
}

/** Where ``text`` sits on a page: rectangles to mark and a PDF-space top to scroll to. */
async function locateCitedText(
	pdfDocument: PDFDocumentProxy,
	pageNumber: number,
	text: string
): Promise<{ rects: PercentRect[]; top: number } | null> {
	const page = await pdfDocument.getPage(pageNumber);
	const content = await page.getTextContent();
	const items = content.items.filter((item): item is TextItem => "str" in item);
	const range = findTextOnPage(items, text);
	if (!range) return null;
	const rects = citedRects(items, range, page.getViewport({ scale: 1 }));
	const first = items[range.first];
	const lineHeight = first.height || Math.hypot(first.transform[2], first.transform[3]);
	// A line and a half above the text, so it does not sit on the panel edge.
	return { rects, top: first.transform[5] + lineHeight * 2.5 };
}

interface PinchPreview {
	baseScale: number;
	scaleFactor: number;
	startClientX: number;
	startClientY: number;
	clientX: number;
	clientY: number;
}

// PDFViewer clamps committed scales to these same limits internally.
const MIN_PDF_SCALE = 0.1;
const MAX_PDF_SCALE = 25;
const ZOOM_RENDER_DELAY_MS = 500;
const MOBILE_MAX_CANVAS_PIXELS = 5 * 1024 * 1024;
const DESKTOP_MAX_CANVAS_PIXELS = 16 * 1024 * 1024;
const DISABLED_LAYER_MODE = 0;

function getTouchMidpoint(touches: TouchList): [number, number] | null {
	const first = touches.item(0);
	const second = touches.item(1);
	if (!first || !second) return null;
	return [(first.clientX + second.clientX) / 2, (first.clientY + second.clientY) / 2];
}

export function PdfViewer({
	pdfUrl,
	isPublic = false,
	toolbarActions,
	zoomControlsContainer,
	rangeRequests = false,
	initialPage,
	highlightText,
}: PdfViewerProps) {
	const [numPages, setNumPages] = useState(0);
	const [loading, setLoading] = useState(true);
	const [loadError, setLoadError] = useState<string | null>(null);
	const [retryKey, setRetryKey] = useState(0);
	const viewerHostRef = useRef<HTMLDivElement>(null);
	const viewerElementRef = useRef<HTMLDivElement>(null);
	const pdfViewerRef = useRef<EmbeddedPdfViewer | null>(null);
	const pdfDocumentRef = useRef<PDFDocumentProxy | null>(null);
	const citedMarkRef = useRef<CitedMark | null>(null);
	// Read when the document lays out, so a new citation of the same file moves
	// the open view instead of reloading the file.
	const initialPageRef = useRef(initialPage);
	const highlightTextRef = useRef(highlightText);
	initialPageRef.current = initialPage;
	highlightTextRef.current = highlightText;

	const showCitedPage = useCallback(async (page?: number, text?: string) => {
		const pdfViewer = pdfViewerRef.current;
		const pdfDocument = pdfDocumentRef.current;
		const viewerElement = viewerElementRef.current;
		if (!pdfViewer || !pdfDocument || !viewerElement) return;
		citedMarkRef.current = null;
		clearCitedMarks(viewerElement);
		const target = pageToOpen(page, pdfDocument.numPages);
		if (!target) return;
		pdfViewer.currentPageNumber = target;
		if (!text) return;
		try {
			const found = await locateCitedText(pdfDocument, target, text);
			// A newer citation or another file replaced this one meanwhile.
			if (!found || pdfDocumentRef.current !== pdfDocument) return;
			if (page !== initialPageRef.current || text !== highlightTextRef.current) return;
			citedMarkRef.current = { pageNumber: target, rects: found.rects };
			const pageDiv = pdfViewer.getPageView(target - 1)?.div as HTMLElement | undefined;
			if (pageDiv) drawCitedMark(pageDiv, found.rects);
			pdfViewer.scrollPageIntoView({
				pageNumber: target,
				destArray: [null, { name: "XYZ" }, null, found.top, null],
			});
		} catch {
			// Finding the text is a nicety; the page itself is already shown.
		}
	}, []);

	useEffect(() => {
		void retryKey;
		const container = viewerHostRef.current;
		const viewerElement = viewerElementRef.current;
		if (!container || !viewerElement) return;

		const controller = new AbortController();
		let disposed = false;
		let loadingTask: PDFDocumentLoadingTask | null = null;
		let pdfDocument: PDFDocumentProxy | null = null;
		let pdfViewer: EmbeddedPdfViewer | null = null;
		let touchManager: TouchManagerInstance | null = null;
		let resizeObserver: ResizeObserver | null = null;
		let resizeFrame: number | null = null;
		let pinchFrame: number | null = null;
		let pinchPreview: PinchPreview | null = null;
		let latestTouchMidpoint: [number, number] | null = null;
		let eventBus: InstanceType<typeof import("pdfjs-dist/web/pdf_viewer.mjs")["EventBus"]> | null =
			null;
		let handlePagesInit: (() => void) | null = null;
		let handlePageRendered: ((event: PageRenderedEvent) => void) | null = null;

		setLoading(true);
		setLoadError(null);
		setNumPages(0);

		const clearPinchPreview = () => {
			if (pinchFrame !== null) {
				cancelAnimationFrame(pinchFrame);
				pinchFrame = null;
			}
			pinchPreview = null;
			latestTouchMidpoint = null;
			viewerElement.style.removeProperty("transform");
			viewerElement.style.removeProperty("transform-origin");
			viewerElement.style.removeProperty("will-change");
		};

		const initialize = async () => {
			try {
				// The viewer component build expects the PDF.js display API on globalThis.
				(
					globalThis as typeof globalThis & {
						pdfjsLib: typeof pdfjsLib;
					}
				).pdfjsLib = pdfjsLib;

				const viewerModulePromise = import("pdfjs-dist/web/pdf_viewer.mjs");
				// With range requests this first call is a one-byte probe: it refreshes
				// an expired session (pdf.js cannot) and shows whether ranges are served.
				const responsePromise = authenticatedFetch(pdfUrl, {
					skipAuthRedirect: true,
					signal: controller.signal,
					...(rangeRequests ? { headers: { Range: "bytes=0-0" } } : {}),
				});
				const [viewerModule, response] = await Promise.all([viewerModulePromise, responsePromise]);

				if (!response.ok) {
					throw new Error(`Server returned ${response.status} while retrieving the PDF`);
				}

				let source: Parameters<typeof pdfjsLib.getDocument>[0];
				if (rangeRequests && response.status === 206) {
					await response.body?.cancel();
					source = {
						url: pdfUrl,
						withCredentials: true,
						httpHeaders: getAuthHeaders(),
						rangeChunkSize: RANGE_CHUNK_BYTES,
						disableAutoFetch: true,
						disableStream: true,
					};
				} else {
					source = { data: await response.arrayBuffer() };
				}
				if (disposed) return;

				const isMobile = window.matchMedia("(max-width: 1023px)").matches;
				eventBus = new viewerModule.EventBus();
				const linkService = new viewerModule.PDFLinkService({ eventBus });
				pdfViewer = new viewerModule.PDFViewer({
					container,
					viewer: viewerElement,
					eventBus,
					linkService,
					textLayerMode: DISABLED_LAYER_MODE,
					annotationMode: DISABLED_LAYER_MODE,
					maxCanvasPixels: isMobile ? MOBILE_MAX_CANVAS_PIXELS : DESKTOP_MAX_CANVAS_PIXELS,
					maxCanvasDim: isMobile ? 8192 : 16384,
					enableDetailCanvas: true,
					enableOptimizedPartialRendering: true,
					imagesRightClickMinSize: -1,
					supportsPinchToZoom: false,
					minDurationToUpdateCanvas: ZOOM_RENDER_DELAY_MS,
					abortSignal: controller.signal,
				} as PDFViewerOptionsWithSignal) as EmbeddedPdfViewer;
				pdfViewerRef.current = pdfViewer;
				linkService.setViewer(pdfViewer);

				handlePagesInit = () => {
					if (disposed || !pdfViewer || !pdfDocument) return;
					viewerElement.classList.toggle("multiple-pages", pdfDocument.numPages > 1);
					viewerElement.style.setProperty("--pdf-pages-count", `"${pdfDocument.numPages}"`);
					pdfViewer.currentScaleValue = "page-width";
					container.scrollTop = 0;
					// Jump before the first page paints, so with range requests only the
					// cited page's bytes are fetched; the effect below then marks the text.
					const target = pageToOpen(initialPageRef.current, pdfDocument.numPages);
					if (target && target > 1) pdfViewer.currentPageNumber = target;
					setNumPages(pdfDocument.numPages);
				};
				handlePageRendered = ({ isDetailView, pageNumber, source }: PageRenderedEvent) => {
					if (disposed || isDetailView) return;
					setLoading(false);
					// A re-rendered page (zoom, or scrolled back into view) drops the mark.
					const mark = citedMarkRef.current;
					if (mark && mark.pageNumber === pageNumber && source?.div) {
						drawCitedMark(source.div, mark.rects);
					}
				};
				eventBus.on("pagesinit", handlePagesInit);
				eventBus.on("pagerendered", handlePageRendered);

				loadingTask = pdfjsLib.getDocument(source);
				pdfDocument = await loadingTask.promise;
				if (disposed) {
					await pdfDocument.destroy();
					return;
				}
				pdfDocumentRef.current = pdfDocument;

				linkService.setDocument(pdfDocument);
				pdfViewer.setDocument(pdfDocument);

				const recordTouchMidpoint = (event: TouchEvent) => {
					latestTouchMidpoint = getTouchMidpoint(event.touches);
				};
				container.addEventListener("touchstart", recordTouchMidpoint, {
					capture: true,
					passive: true,
					signal: controller.signal,
				});
				container.addEventListener("touchmove", recordTouchMidpoint, {
					capture: true,
					passive: true,
					signal: controller.signal,
				});

				const TouchManager = (
					pdfjsLib as typeof pdfjsLib & {
						TouchManager: TouchManagerConstructor;
					}
				).TouchManager;
				touchManager = new TouchManager({
					container,
					isPinchingDisabled: () => !pdfViewer || pdfViewer.pagesCount === 0,
					onPinchStart: () => {
						if (!pdfViewer || !latestTouchMidpoint) return;
						const [clientX, clientY] = latestTouchMidpoint;
						const viewerRect = viewerElement.getBoundingClientRect();
						pinchPreview = {
							baseScale: pdfViewer.currentScale,
							scaleFactor: 1,
							startClientX: clientX,
							startClientY: clientY,
							clientX,
							clientY,
						};
						viewerElement.style.transformOrigin = `${clientX - viewerRect.left}px ${
							clientY - viewerRect.top
						}px`;
						viewerElement.style.willChange = "transform";
					},
					onPinching: (_origin, previousDistance, distance) => {
						if (!pinchPreview || !latestTouchMidpoint || previousDistance <= 0) return;
						const targetScale = Math.max(
							MIN_PDF_SCALE,
							Math.min(
								MAX_PDF_SCALE,
								pinchPreview.baseScale * pinchPreview.scaleFactor * (distance / previousDistance)
							)
						);
						pinchPreview.scaleFactor = targetScale / pinchPreview.baseScale;
						pinchPreview.clientX = latestTouchMidpoint[0];
						pinchPreview.clientY = latestTouchMidpoint[1];

						if (pinchFrame !== null) return;
						pinchFrame = requestAnimationFrame(() => {
							pinchFrame = null;
							if (!pinchPreview) return;
							const translateX = pinchPreview.clientX - pinchPreview.startClientX;
							const translateY = pinchPreview.clientY - pinchPreview.startClientY;
							viewerElement.style.transform = `translate3d(${translateX}px, ${translateY}px, 0) scale(${pinchPreview.scaleFactor})`;
						});
					},
					onPinchEnd: () => {
						const preview = pinchPreview;
						if (!pdfViewer || !preview) {
							clearPinchPreview();
							return;
						}

						const translateX = preview.clientX - preview.startClientX;
						const translateY = preview.clientY - preview.startClientY;
						if (preview.scaleFactor !== 1) {
							const containerRect = container.getBoundingClientRect();
							pdfViewer.updateScale({
								drawingDelay: ZOOM_RENDER_DELAY_MS,
								scaleFactor: preview.scaleFactor,
								origin: [
									container.offsetLeft + preview.startClientX - containerRect.left,
									container.offsetTop + preview.startClientY - containerRect.top,
								],
							});
						}
						container.scrollLeft -= translateX;
						container.scrollTop -= translateY;
						clearPinchPreview();
					},
					signal: controller.signal,
				});

				resizeObserver = new ResizeObserver(() => {
					if (pdfViewer?.currentScaleValue !== "page-width") return;
					if (resizeFrame !== null) cancelAnimationFrame(resizeFrame);
					resizeFrame = requestAnimationFrame(() => {
						resizeFrame = null;
						if (pdfViewer?.currentScaleValue === "page-width") {
							pdfViewer.currentScaleValue = "page-width";
						}
					});
				});
				resizeObserver.observe(container);
			} catch (error: unknown) {
				if (disposed) return;
				setLoadError(error instanceof Error ? error.message : "Failed to load PDF");
				setLoading(false);
			}
		};

		void initialize();

		return () => {
			disposed = true;
			controller.abort();
			if (resizeFrame !== null) cancelAnimationFrame(resizeFrame);
			resizeObserver?.disconnect();
			touchManager?.destroy();
			clearPinchPreview();
			if (eventBus && handlePagesInit) eventBus.off("pagesinit", handlePagesInit);
			if (eventBus && handlePageRendered) eventBus.off("pagerendered", handlePageRendered);
			pdfViewer?.setDocument(null);
			pdfViewerRef.current = null;
			pdfDocumentRef.current = null;
			citedMarkRef.current = null;
			viewerElement.classList.remove("multiple-pages");
			viewerElement.style.removeProperty("--pdf-pages-count");
			if (pdfDocument) {
				void pdfDocument.destroy();
			} else {
				void loadingTask?.destroy();
			}
		};
	}, [pdfUrl, retryKey, rangeRequests]);

	// Once laid out, and for each later citation of the same open file: go to
	// the cited page and mark the cited text, without reloading the file.
	useEffect(() => {
		if (numPages > 0) void showCitedPage(initialPage, highlightText);
	}, [initialPage, highlightText, numPages, showCitedPage]);

	const zoomIn = useCallback(() => {
		pdfViewerRef.current?.increaseScale({ drawingDelay: ZOOM_RENDER_DELAY_MS });
	}, []);

	const zoomOut = useCallback(() => {
		pdfViewerRef.current?.decreaseScale({ drawingDelay: ZOOM_RENDER_DELAY_MS });
	}, []);

	const zoomControls = (
		<div className="hidden items-center gap-1 lg:flex">
			<Button
				variant="ghost"
				size="icon"
				onClick={zoomOut}
				disabled={loading}
				className="size-6 shrink-0 rounded-full text-muted-foreground"
			>
				<ZoomOutIcon className="size-4" />
				<span className="sr-only">Zoom out</span>
			</Button>
			<Button
				variant="ghost"
				size="icon"
				onClick={zoomIn}
				disabled={loading}
				className="size-6 shrink-0 rounded-full text-muted-foreground"
			>
				<ZoomInIcon className="size-4" />
				<span className="sr-only">Zoom in</span>
			</Button>
		</div>
	);

	if (loadError) {
		return (
			<div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
				<p className="font-medium text-foreground">Failed to load PDF</p>
				<p className="text-sm text-muted-foreground">{loadError}</p>
				<Button variant="secondary" size="sm" onClick={() => setRetryKey((key) => key + 1)}>
					Try again
				</Button>
			</div>
		);
	}

	return (
		<div className="flex h-full flex-col">
			{numPages > 0 && zoomControlsContainer
				? createPortal(zoomControls, zoomControlsContainer)
				: null}
			{numPages > 0 && zoomControlsContainer === undefined ? (
				<div
					className={`flex shrink-0 select-none items-center border-b px-4 py-2 ${isPublic ? "bg-main-panel" : "bg-sidebar"}`}
				>
					<div className="flex-1" aria-hidden="true" />
					{zoomControls}
					<div className="flex flex-1 items-center justify-end gap-1">{toolbarActions}</div>
				</div>
			) : null}

			<div className="relative min-h-0 flex-1">
				<div
					ref={viewerHostRef}
					data-vaul-no-drag=""
					className="absolute inset-0 overflow-auto bg-white"
				>
					<div ref={viewerElementRef} className="pdfViewer surfsense-pdf-viewer" />
				</div>
				{loading ? (
					<div
						className={`pointer-events-none absolute inset-0 flex items-center justify-center ${isPublic ? "text-foreground" : "text-sidebar-foreground"}`}
					>
						<Spinner size="md" />
					</div>
				) : null}
			</div>
		</div>
	);
}
