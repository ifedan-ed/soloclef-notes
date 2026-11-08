"use client";

import {
	ChevronLeft,
	ChevronRight,
	ChevronsLeftRight,
	ChevronsRightLeft,
	Download,
	Maximize2,
	Minus,
	Plus,
} from "lucide-react";
import { Transformer } from "markmap-lib";
import { useReducedMotion } from "motion/react";
import { useTranslations } from "next-intl";
import { useTheme } from "next-themes";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import type { ArtifactRendererProps } from "@/features/artifacts/model/renderer";
import { UnviewableFile } from "@/features/file-viewers/unviewable-file";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { useExplain } from "@/features/notebooks/state/explain";
import { cn } from "@/lib/utils";
import { downloadName, embeddedFontCss, mindMapSvg, saveBlob, svgToPng } from "./mindmap-export";
import {
	CHEVRON_GAP,
	CHEVRON_SIZE,
	connectorPath,
	FONT_SIZE,
	fitAll,
	fitWidth,
	type Layout,
	LINE_HEIGHT,
	layoutTree,
	measureTree,
	type NodeBox,
	PAD_X,
	ROOT_FONT_SIZE,
	revealBranch,
	shiftLayout,
	type View,
} from "./mindmap-layout";
import { paletteFor, pillColours } from "./mindmap-theme";
import {
	allExpandable,
	hasCollapsed,
	initiallyExpanded,
	MAX_SOURCE_LENGTH,
	type MindMapNode,
	toggled,
	toMindMapTree,
} from "./mindmap-tree";
import { type FrameNode, useAnimatedLayout } from "./use-animated-layout";
import { usePanZoom } from "./use-pan-zoom";

const transformer = new Transformer([]);
transformer.md.set({ html: false, linkify: false });
transformer.md.renderer.rules.link_open = () => "";
transformer.md.renderer.rules.link_close = () => "";
transformer.md.renderer.rules.image = (tokens: Array<{ content: string }>, index: number) =>
	transformer.md.utils.escapeHtml(tokens[index]?.content ?? "");

function textContent(html: string): string {
	const document = new DOMParser().parseFromString(html, "text/html");
	return document.body.textContent?.trim() ?? "";
}

function parseMindMap(markdown: string): MindMapNode | null {
	try {
		if (!markdown.trim() || markdown.length > MAX_SOURCE_LENGTH) return null;
		return toMindMapTree(transformer.transform(markdown).root, textContent);
	} catch {
		return null;
	}
}

function textMeasurer(fontFamily: string) {
	const context = document.createElement("canvas").getContext("2d");
	return (text: string, size: number, bold: boolean) => {
		if (!context) return text.length * size * 0.6;
		context.font = `${bold ? 600 : 500} ${size}px ${fontFamily}`;
		// A little slack, so a font that renders a hair wider still fits.
		return context.measureText(text).width + 2;
	};
}

interface MapState {
	expanded: Set<string>;
	layout: Layout;
}

function ScreenReaderTree({ node }: { node: MindMapNode }) {
	return (
		<li>
			<span>{node.label}</span>
			{node.children.length > 0 ? (
				<ul>
					{node.children.map((child) => (
						<ScreenReaderTree key={child.id} node={child} />
					))}
				</ul>
			) : null}
		</li>
	);
}

/**
 * A NotebookLM-style mind map: root on the left, branches flowing right with
 * curved connectors, a chevron on every node with children, root and first
 * level open at first. Drag to pan, pinch or the buttons to zoom. Clicking a
 * topic asks the notebook's chat about it (outside a notebook it opens or
 * closes the topic instead).
 */
export default function MindMapViewer({ manifest }: ArtifactRendererProps) {
	const t = useTranslations("mind_map");
	const tree = useMemo(
		() => parseMindMap(manifest.markdown_representation),
		[manifest.markdown_representation]
	);
	const { resolvedTheme } = useTheme();
	const palette = paletteFor(resolvedTheme === "dark");
	const reducedMotion = useReducedMotion() ?? false;
	const explain = useExplain();

	const frameRef = useRef<HTMLDivElement>(null);
	const [fontFamily, setFontFamily] = useState<string | null>(null);
	const [viewport, setViewport] = useState({ width: 0, height: 0 });
	const [view, setView] = useState<View>({ x: 0, y: 0, k: 1 });
	const [state, setState] = useState<MapState | null>(null);
	const [downloadOpen, setDownloadOpen] = useState(false);
	const { handlers, wasDrag, zoomBy } = usePanZoom(frameRef, setView);
	// The branch just toggled, to bring into view once its layout lands, and a
	// short window in which the view glides there instead of jumping.
	const revealRef = useRef<string | null>(null);
	const [gliding, setGliding] = useState(false);

	// Measure with the page's own font once it has loaded.
	useEffect(() => {
		const element = frameRef.current;
		if (!element) return;
		let cancelled = false;
		const family = getComputedStyle(element).fontFamily || "sans-serif";
		const ready = document.fonts?.ready ?? Promise.resolve();
		void ready.then(() => {
			if (!cancelled) setFontFamily(family);
		});
		const observer = new ResizeObserver(([entry]) => {
			setViewport({ width: entry.contentRect.width, height: entry.contentRect.height });
		});
		observer.observe(element);
		return () => {
			cancelled = true;
			observer.disconnect();
		};
	}, []);

	const boxes = useMemo<Map<string, NodeBox> | null>(
		() => (tree && fontFamily ? measureTree(tree, textMeasurer(fontFamily)) : null),
		[tree, fontFamily]
	);

	// Open with the root and its first level, fitted to the width, once per map.
	const openedFor = useRef<MindMapNode | null>(null);
	useEffect(() => {
		if (!tree || !boxes || viewport.width === 0 || openedFor.current === tree) return;
		openedFor.current = tree;
		const expanded = initiallyExpanded(tree);
		const layout = layoutTree(tree, boxes, expanded);
		setState({ expanded, layout });
		setView(fitWidth(layout.bounds, viewport));
	}, [tree, boxes, viewport]);

	/** Re-lay out with ``next`` open, keeping ``anchorId`` where it is on screen. */
	const applyExpanded = useCallback(
		(next: (expanded: Set<string>) => Set<string>, anchorId: string) => {
			if (!tree || !boxes) return;
			setState((current) => {
				if (!current) return current;
				const expanded = next(current.expanded);
				const raw = layoutTree(tree, boxes, expanded);
				const before = current.layout.byId.get(anchorId);
				const after = raw.byId.get(anchorId);
				const layout =
					before && after ? shiftLayout(raw, before.x - after.x, before.y - after.y) : raw;
				return { expanded, layout };
			});
		},
		[tree, boxes]
	);

	// Opening a branch near the right edge used to leave its children (and
	// their chevrons) off-screen; the view now follows the branch.
	useEffect(() => {
		const nodeId = revealRef.current;
		if (!nodeId || !state) return;
		revealRef.current = null;
		setView((current) => {
			const next = revealBranch(
				current,
				state.layout,
				nodeId,
				state.expanded.has(nodeId),
				viewport
			);
			if (next !== current && !reducedMotion) setGliding(true);
			return next;
		});
	}, [state, viewport, reducedMotion]);
	useEffect(() => {
		if (!gliding) return;
		const timer = setTimeout(() => setGliding(false), 360);
		return () => clearTimeout(timer);
	}, [gliding]);

	const frame = useAnimatedLayout(state?.layout ?? null, reducedMotion);
	const showSpinner = useDelayedFlag(!!tree && !state);

	if (!tree) return <UnviewableFile message={t("unviewable")} />;

	const rootLabel = tree.label;
	const onLabel = (node: MindMapNode) => {
		if (wasDrag()) return;
		if (explain) {
			// On a phone the chat is under this viewer; useExplain moves it aside.
			explain(
				node.depth === 0
					? t("ask_question_root", { topic: node.label })
					: t("ask_question", { topic: node.label, root: rootLabel })
			);
			return;
		}
		if (node.children.length > 0) {
			revealRef.current = node.id;
			applyExpanded((open) => toggled(open, node.id), node.id);
		}
	};
	const onChevron = (node: MindMapNode) => {
		if (wasDrag()) return;
		revealRef.current = node.id;
		applyExpanded((open) => toggled(open, node.id), node.id);
	};

	const canExpandMore = state ? hasCollapsed(tree, state.expanded) : true;
	const toggleAll = () =>
		applyExpanded(() => (canExpandMore ? allExpandable(tree) : new Set<string>()), tree.id);

	const download = async (kind: "png" | "svg") => {
		if (!state) return;
		try {
			const family = fontFamily ?? "sans-serif";
			const svg = mindMapSvg(state.layout, palette, family, await embeddedFontCss(family));
			const blob =
				kind === "svg" ? new Blob([svg], { type: "image/svg+xml" }) : await svgToPng(svg);
			saveBlob(blob, downloadName(manifest.title, kind));
		} catch {
			toast.error(t("download_failed"));
		}
	};

	const byId = new Map(frame.filter((node) => !node.leaving).map((node) => [node.node.id, node]));

	return (
		<div
			data-vaul-no-drag=""
			className="relative h-full min-h-0 overflow-hidden"
			style={{ background: palette.background }}
		>
			<div
				ref={frameRef}
				data-testid="mind-map-canvas"
				className="absolute inset-0 cursor-grab touch-none select-none overflow-hidden active:cursor-grabbing"
				{...handlers}
				onPointerDownCapture={() => setDownloadOpen(false)}
			>
				<div
					className="absolute left-0 top-0 origin-top-left"
					style={{
						transform: `translate(${view.x}px, ${view.y}px) scale(${view.k})`,
						transition: gliding ? "transform 320ms cubic-bezier(0.4, 0, 0.2, 1)" : undefined,
						fontFamily: fontFamily ?? undefined,
					}}
				>
					<svg
						aria-hidden="true"
						focusable="false"
						className="absolute left-0 top-0 overflow-visible"
						width={1}
						height={1}
					>
						{frame.map((placed) => {
							const parent = placed.parentId ? byId.get(placed.parentId) : undefined;
							if (!parent) return null;
							return (
								<path
									key={placed.node.id}
									d={connectorPath(parent, placed)}
									fill="none"
									stroke={palette.connector}
									strokeWidth={1.5}
									opacity={placed.opacity}
								/>
							);
						})}
					</svg>
					{frame.map((placed) => (
						<MindMapPill
							key={placed.node.id}
							placed={placed}
							palette={palette}
							askable={!!explain}
							labels={{
								ask: t("ask_about", { label: placed.node.label }),
								expand: t("expand_node", { label: placed.node.label }),
								collapse: t("collapse_node", { label: placed.node.label }),
							}}
							onLabel={() => onLabel(placed.node)}
							onChevron={() => onChevron(placed.node)}
						/>
					))}
				</div>
			</div>

			{showSpinner ? (
				<div className="absolute inset-0 flex items-center justify-center" aria-busy="true">
					<Spinner size="lg" />
				</div>
			) : null}

			{state ? (
				<div
					role="toolbar"
					aria-label={t("controls")}
					aria-orientation="vertical"
					className="absolute bottom-[max(0.75rem,env(safe-area-inset-bottom))] right-3 flex flex-col gap-1 rounded-2xl border bg-background/95 p-1 shadow-sm"
				>
					<ControlButton
						label={canExpandMore ? t("expand_all") : t("collapse_all")}
						onClick={toggleAll}
					>
						{canExpandMore ? (
							<ChevronsLeftRight className="size-4" />
						) : (
							<ChevronsRightLeft className="size-4" />
						)}
					</ControlButton>
					<ControlButton label={t("zoom_in")} onClick={() => zoomBy(1.25)}>
						<Plus className="size-4" />
					</ControlButton>
					<ControlButton label={t("zoom_out")} onClick={() => zoomBy(0.8)}>
						<Minus className="size-4" />
					</ControlButton>
					<ControlButton
						label={t("fit")}
						onClick={() => setView(fitAll(state.layout.bounds, viewport))}
					>
						<Maximize2 className="size-4" />
					</ControlButton>
					<ControlButton
						label={t("download")}
						onClick={() => setDownloadOpen((open) => !open)}
						expanded={downloadOpen}
					>
						<Download className="size-4" />
					</ControlButton>
				</div>
			) : null}

			{state && downloadOpen ? (
				// Inline, not portalled: on a phone this viewer sits in a drawer
				// whose overlay would cover a portalled menu.
				<div
					role="menu"
					aria-label={t("download")}
					className="absolute bottom-[max(0.75rem,env(safe-area-inset-bottom))] right-16 flex min-w-40 flex-col rounded-xl border bg-popover p-1 text-popover-foreground shadow-md"
					onKeyDown={(event) => {
						if (event.key === "Escape") setDownloadOpen(false);
					}}
				>
					{(["png", "svg"] as const).map((kind) => (
						<button
							key={kind}
							type="button"
							role="menuitem"
							className="rounded-md px-3 py-2 text-left text-sm hover:bg-accent focus-visible:bg-accent focus-visible:outline-none"
							onClick={() => {
								setDownloadOpen(false);
								void download(kind);
							}}
						>
							{kind === "png" ? t("download_png") : t("download_svg")}
						</button>
					))}
				</div>
			) : null}

			<div className="sr-only">
				<ul aria-label={t("label")}>
					<ScreenReaderTree node={tree} />
				</ul>
			</div>
		</div>
	);
}

function ControlButton({
	label,
	onClick,
	children,
	expanded,
}: {
	label: string;
	onClick: () => void;
	children: React.ReactNode;
	expanded?: boolean;
}) {
	return (
		<Button
			aria-haspopup={expanded === undefined ? undefined : "menu"}
			aria-expanded={expanded}
			type="button"
			variant="ghost"
			size="icon"
			className="size-9 rounded-xl"
			aria-label={label}
			title={label}
			onClick={onClick}
		>
			{children}
		</Button>
	);
}

function MindMapPill({
	placed,
	palette,
	askable,
	labels,
	onLabel,
	onChevron,
}: {
	placed: FrameNode;
	palette: ReturnType<typeof paletteFor>;
	askable: boolean;
	labels: { ask: string; expand: string; collapse: string };
	onLabel: () => void;
	onChevron: () => void;
}) {
	const { node } = placed;
	const colours = pillColours(palette, node.depth);
	const root = node.depth === 0;
	const hasChildren = node.children.length > 0;
	return (
		<div
			className={cn("absolute left-0 top-0", placed.leaving && "pointer-events-none")}
			style={{
				transform: `translate(${placed.x}px, ${placed.y}px)`,
				width: placed.width,
				height: placed.height,
				opacity: placed.opacity,
			}}
			data-mind-map-node={node.id}
		>
			<button
				type="button"
				onClick={onLabel}
				aria-label={askable ? labels.ask : undefined}
				title={askable ? labels.ask : undefined}
				tabIndex={placed.leaving ? -1 : 0}
				className="flex h-full w-full flex-col justify-center border text-left transition-[filter] hover:brightness-95 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring dark:hover:brightness-110"
				style={{
					background: colours.fill,
					borderColor: colours.stroke,
					color: colours.text,
					borderRadius: Math.min(18, placed.height / 2),
					paddingInline: PAD_X,
					fontSize: root ? ROOT_FONT_SIZE : FONT_SIZE,
					fontWeight: root ? 600 : 500,
					lineHeight: `${LINE_HEIGHT}px`,
				}}
			>
				{placed.lines.map((line, index) => (
					// biome-ignore lint/suspicious/noArrayIndexKey: a wrapped label's lines have no other identity.
					<span key={index} className="block whitespace-nowrap">
						{line}
					</span>
				))}
			</button>
			{hasChildren ? (
				<button
					type="button"
					onClick={onChevron}
					aria-expanded={placed.expanded}
					aria-label={placed.expanded ? labels.collapse : labels.expand}
					title={placed.expanded ? labels.collapse : labels.expand}
					tabIndex={placed.leaving ? -1 : 0}
					className="absolute flex items-center justify-center rounded-full border before:absolute before:-inset-2.5 before:content-[''] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
					style={{
						left: placed.width + CHEVRON_GAP,
						top: (placed.height - CHEVRON_SIZE) / 2,
						width: CHEVRON_SIZE,
						height: CHEVRON_SIZE,
						background: palette.chevronFill,
						borderColor: palette.chevronStroke,
						color: palette.chevronIcon,
					}}
				>
					{placed.expanded ? (
						<ChevronLeft className="size-3.5" aria-hidden />
					) : (
						<ChevronRight className="size-3.5" aria-hidden />
					)}
				</button>
			) : null}
		</div>
	);
}
