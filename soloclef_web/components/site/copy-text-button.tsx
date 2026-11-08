"use client";

import { IconCheck, IconCopy } from "@tabler/icons-react";
import { useEffect, useRef, useState } from "react";

/**
 * Copies a short text and says so in place for two seconds. No toast and no
 * dialog: the button's own label changes, and a polite live region announces
 * it to screen readers.
 */
export function CopyTextButton({ text, label = "Copy" }: { text: string; label?: string }) {
	const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
	const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

	useEffect(
		() => () => {
			if (timer.current) clearTimeout(timer.current);
		},
		[]
	);

	const copy = async () => {
		try {
			await navigator.clipboard.writeText(text);
			setState("copied");
		} catch {
			setState("failed");
		}
		if (timer.current) clearTimeout(timer.current);
		timer.current = setTimeout(() => setState("idle"), 2000);
	};

	return (
		<button
			type="button"
			onClick={copy}
			className="ss-home-btn ss-home-btn-secondary ss-home-btn-sm"
			data-testid="copy-email"
		>
			{state === "copied" ? (
				<IconCheck aria-hidden="true" className="size-4" />
			) : (
				<IconCopy aria-hidden="true" className="size-4" />
			)}
			<span>{state === "copied" ? "Copied" : state === "failed" ? "Select and copy" : label}</span>
			<span className="sr-only" aria-live="polite">
				{state === "copied" ? "Copied to the clipboard" : ""}
			</span>
		</button>
	);
}
