"use client";

import { useEffect, useRef } from "react";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";

interface ClearAllConfirmProps {
	title: string;
	busy: boolean;
	onCancel: () => void;
	onConfirm: () => void;
}

/**
 * "Clear all" asks in the panel's own header rather than in a dialog: a dialog
 * would take focus out of the Notifications popover or drawer and close it.
 */
export function ClearAllConfirm({ title, busy, onCancel, onConfirm }: ClearAllConfirmProps) {
	const cancelRef = useRef<HTMLButtonElement>(null);
	// The button that asked is gone: keep the keyboard here, on the safe choice.
	useEffect(() => {
		cancelRef.current?.focus();
	}, []);

	return (
		<fieldset
			aria-label={title}
			className="m-0 flex min-w-0 flex-1 items-center gap-2 border-0 p-0"
		>
			<p className="min-w-0 flex-1 truncate text-sm font-medium">{title}</p>
			<Button
				ref={cancelRef}
				type="button"
				variant="ghost"
				size="sm"
				onClick={onCancel}
				disabled={busy}
				className="h-8 shrink-0 px-2 text-xs"
			>
				Cancel
			</Button>
			<Button
				type="button"
				variant="destructive"
				size="sm"
				onClick={onConfirm}
				disabled={busy}
				className="h-8 shrink-0 gap-1.5 px-2 text-xs"
			>
				{busy ? <Spinner size="xs" /> : null}
				Clear all
			</Button>
		</fieldset>
	);
}
