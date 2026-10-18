"use client";

import {
	type Dispatch,
	type PointerEvent as ReactPointerEvent,
	type RefObject,
	type SetStateAction,
	useCallback,
	useEffect,
	useRef,
} from "react";
import { type View, zoomAt } from "./mindmap-layout";

/** Movement (px) after which a press is a drag, not a click. */
const DRAG_THRESHOLD = 5;

/**
 * Drag to pan (mouse, pen or one finger), pinch or Ctrl/⌘+wheel to zoom,
 * plain wheel/trackpad scroll to pan. ``wasDrag()`` tells a click handler to
 * ignore the click that ends a drag.
 */
export function usePanZoom(
	containerRef: RefObject<HTMLDivElement | null>,
	setView: Dispatch<SetStateAction<View>>
) {
	const pointers = useRef(new Map<number, { x: number; y: number }>());
	const start = useRef<{ x: number; y: number } | null>(null);
	const dragging = useRef(false);
	const pinch = useRef<{ distance: number } | null>(null);

	const local = useCallback(
		(clientX: number, clientY: number) => {
			const rect = containerRef.current?.getBoundingClientRect();
			return { x: clientX - (rect?.left ?? 0), y: clientY - (rect?.top ?? 0) };
		},
		[containerRef]
	);

	const onPointerDown = useCallback((event: ReactPointerEvent<HTMLDivElement>) => {
		if (event.pointerType === "mouse" && event.button !== 0) return;
		pointers.current.set(event.pointerId, { x: event.clientX, y: event.clientY });
		if (pointers.current.size === 1) {
			start.current = { x: event.clientX, y: event.clientY };
			dragging.current = false;
		} else if (pointers.current.size === 2) {
			const [a, b] = [...pointers.current.values()];
			pinch.current = { distance: Math.hypot(a.x - b.x, a.y - b.y) };
			dragging.current = true;
		}
	}, []);

	const onPointerMove = useCallback(
		(event: ReactPointerEvent<HTMLDivElement>) => {
			const previous = pointers.current.get(event.pointerId);
			if (!previous) return;
			const current = { x: event.clientX, y: event.clientY };
			pointers.current.set(event.pointerId, current);

			if (pointers.current.size >= 2 && pinch.current) {
				const [a, b] = [...pointers.current.values()];
				const distance = Math.hypot(a.x - b.x, a.y - b.y);
				const factor = distance / Math.max(pinch.current.distance, 1);
				pinch.current = { distance };
				const mid = local((a.x + b.x) / 2, (a.y + b.y) / 2);
				setView((view) => zoomAt(view, factor, mid));
				return;
			}
			if (!dragging.current && start.current) {
				const moved = Math.hypot(current.x - start.current.x, current.y - start.current.y);
				if (moved < DRAG_THRESHOLD) return;
				dragging.current = true;
				// Capture only once it is a drag, so a plain click still reaches its button.
				event.currentTarget.setPointerCapture?.(event.pointerId);
			}
			if (dragging.current) {
				const dx = current.x - previous.x;
				const dy = current.y - previous.y;
				setView((view) => ({ ...view, x: view.x + dx, y: view.y + dy }));
			}
		},
		[local, setView]
	);

	const onPointerUp = useCallback((event: ReactPointerEvent<HTMLDivElement>) => {
		pointers.current.delete(event.pointerId);
		if (pointers.current.size < 2) pinch.current = null;
		if (pointers.current.size === 0) start.current = null;
	}, []);

	// A non-passive wheel listener, so the page itself does not scroll or zoom.
	useEffect(() => {
		const element = containerRef.current;
		if (!element) return;
		const onWheel = (event: WheelEvent) => {
			event.preventDefault();
			if (event.ctrlKey || event.metaKey) {
				const factor = Math.exp(-event.deltaY * 0.01);
				const at = local(event.clientX, event.clientY);
				setView((view) => zoomAt(view, factor, at));
			} else {
				setView((view) => ({ ...view, x: view.x - event.deltaX, y: view.y - event.deltaY }));
			}
		};
		element.addEventListener("wheel", onWheel, { passive: false });
		return () => element.removeEventListener("wheel", onWheel);
	}, [containerRef, local, setView]);

	const wasDrag = useCallback(() => {
		const was = dragging.current;
		dragging.current = false;
		return was;
	}, []);

	const zoomBy = useCallback(
		(factor: number) => {
			const rect = containerRef.current?.getBoundingClientRect();
			const at = { x: (rect?.width ?? 0) / 2, y: (rect?.height ?? 0) / 2 };
			setView((view) => zoomAt(view, factor, at));
		},
		[containerRef, setView]
	);

	return {
		handlers: {
			onPointerDown,
			onPointerMove,
			onPointerUp,
			onPointerCancel: onPointerUp,
		},
		wasDrag,
		zoomBy,
	};
}
