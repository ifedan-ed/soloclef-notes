"use client";

import { useRef, useState } from "react";
import {
	advanceRotation,
	claimOpenOffset,
	type RotationState,
	startRotation,
} from "../lib/suggestion-rotation";

/**
 * Which group of three ideas a Studio dialog shows. Each dialog key (output
 * type, format, language) claims its starting group once per open, so the
 * chips never jump after the first paint.
 */
export function useSuggestionRotation(key: string) {
	const claimed = useRef(new Map<string, number>());
	if (!claimed.current.has(key)) claimed.current.set(key, claimOpenOffset(key));
	const [states, setStates] = useState<Record<string, RotationState>>({});
	const state = states[key] ?? startRotation(claimed.current.get(key) ?? 0);

	return {
		offset: state.offset,
		/** Show the next group; true when the pool is used up and needs refreshing. */
		advance(poolSize: number): boolean {
			const next = advanceRotation(state, poolSize);
			if (!next.exhausted) setStates((all) => ({ ...all, [key]: next.state }));
			return next.exhausted;
		},
		/** Start again from the first group, after a fresh pool arrived. */
		restart() {
			setStates((all) => ({ ...all, [key]: startRotation(0) }));
		},
	};
}
