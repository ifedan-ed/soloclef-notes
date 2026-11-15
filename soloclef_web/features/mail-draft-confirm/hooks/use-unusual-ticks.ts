"use client";

import { useCallback, useMemo, useState } from "react";
import type { SendConfirmation } from "../api/mail-draft-api";

/**
 * The person's ticks on a confirmation's unusual points. They belong to one
 * confirmation: a new one (after a refusal, or the card drawn again) starts
 * with nothing ticked, because what was ticked was what that one listed.
 */
export function useUnusualTicks(confirmation: SendConfirmation | null) {
	const [ticked, setTicked] = useState<{ token: string; codes: ReadonlySet<string> }>({
		token: "",
		codes: new Set(),
	});
	const token = confirmation?.token ?? "";
	const codes = ticked.token === token ? ticked.codes : new Set<string>();
	const points = confirmation?.unusual ?? [];

	const toggle = useCallback(
		(code: string, on: boolean) => {
			setTicked((previous) => {
				const next = new Set(previous.token === token ? previous.codes : []);
				if (on) next.add(code);
				else next.delete(code);
				return { token, codes: next };
			});
		},
		[token]
	);

	const allTicked = useMemo(() => points.every((point) => codes.has(point.code)), [points, codes]);
	return { points, ticked: codes, toggle, allTicked };
}
