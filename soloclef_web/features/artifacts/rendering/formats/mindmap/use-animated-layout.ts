"use client";

import { useEffect, useRef, useState } from "react";
import type { Layout, PlacedNode } from "./mindmap-layout";

const DURATION_MS = 280;

/** A node as drawn in one animation frame. */
export interface FrameNode extends PlacedNode {
	opacity: number;
	/** Leaving: collapsing into its parent. */
	leaving: boolean;
}

interface Point {
	x: number;
	y: number;
}

function ease(t: number): number {
	return 1 - (1 - t) ** 3;
}

/**
 * Moves nodes from where they were drawn to where ``layout`` puts them.
 * New nodes grow out of their parent's position; collapsed ones slide back
 * into it and fade. With reduced motion the new layout is drawn at once.
 */
export function useAnimatedLayout(layout: Layout | null, reducedMotion: boolean): FrameNode[] {
	const [frame, setFrame] = useState<FrameNode[]>([]);
	const drawn = useRef(new Map<string, { node: FrameNode; at: Point }>());
	const raf = useRef<number | null>(null);

	useEffect(() => {
		if (raf.current !== null) cancelAnimationFrame(raf.current);
		if (!layout) {
			drawn.current = new Map();
			setFrame([]);
			return;
		}
		const settled: FrameNode[] = layout.nodes.map((node) => ({
			...node,
			opacity: 1,
			leaving: false,
		}));
		const previous = drawn.current;
		const settle = () => {
			drawn.current = new Map(settled.map((node) => [node.node.id, { node, at: node }]));
			setFrame(settled);
		};
		if (reducedMotion || previous.size === 0) {
			settle();
			return;
		}

		// Where each node starts: where it was, else where its nearest drawn ancestor was.
		const startOf = (placed: PlacedNode): Point => {
			const seen = previous.get(placed.node.id);
			if (seen) return seen.at;
			let parentId = placed.parentId;
			while (parentId) {
				const parent = previous.get(parentId);
				if (parent) return { x: parent.at.x, y: parent.at.y };
				parentId = layout.byId.get(parentId)?.parentId ?? null;
			}
			return placed;
		};
		const entering = settled.map((node) => ({
			node,
			from: startOf(node),
			fresh: !previous.has(node.node.id),
		}));
		// Nodes no longer shown collapse into their nearest ancestor that still is.
		const leaving: { node: FrameNode; from: Point; to: Point }[] = [];
		for (const [id, { node, at }] of previous) {
			if (layout.byId.has(id)) continue;
			let parentId = node.parentId;
			let target: Point = at;
			while (parentId) {
				const kept = layout.byId.get(parentId);
				if (kept) {
					target = { x: kept.x, y: kept.y };
					break;
				}
				parentId = previous.get(parentId)?.node.parentId ?? null;
			}
			leaving.push({ node: { ...node, leaving: true }, from: at, to: target });
		}

		const started = performance.now();
		const step = (now: number) => {
			const t = ease(Math.min(1, (now - started) / DURATION_MS));
			const lerp = (a: number, b: number) => a + (b - a) * t;
			const next: FrameNode[] = [
				...leaving.map(({ node, from, to }) => ({
					...node,
					x: lerp(from.x, to.x),
					y: lerp(from.y, to.y),
					opacity: 1 - t,
				})),
				...entering.map(({ node, from, fresh }) => ({
					...node,
					x: lerp(from.x, node.x),
					y: lerp(from.y, node.y),
					opacity: fresh ? t : 1,
				})),
			];
			if (t < 1) {
				drawn.current = new Map(
					next.filter((node) => !node.leaving).map((node) => [node.node.id, { node, at: node }])
				);
				setFrame(next);
				raf.current = requestAnimationFrame(step);
			} else {
				raf.current = null;
				settle();
			}
		};
		raf.current = requestAnimationFrame(step);
		return () => {
			if (raf.current !== null) cancelAnimationFrame(raf.current);
		};
	}, [layout, reducedMotion]);

	return frame;
}
