"use client";

import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from "react";
import { loadTurnstile, type TurnstileApi } from "../lib/turnstile-script";

export interface TurnstileWidgetHandle {
	/** Ask for a fresh token: each one is accepted by the server only once. */
	reset: () => void;
}

interface TurnstileWidgetProps {
	siteKey: string;
	/** Shown in Cloudflare's analytics, e.g. "login". */
	action: string;
	onToken: (token: string | null) => void;
	/** The script could not load or the check errored. */
	onUnavailable?: (unavailable: boolean) => void;
}

/**
 * Cloudflare Turnstile, rendered explicitly into a box that already has the
 * widget's height, so the form below never jumps when it appears.
 */
export const TurnstileWidget = forwardRef<TurnstileWidgetHandle, TurnstileWidgetProps>(
	function TurnstileWidget({ siteKey, action, onToken, onUnavailable }, ref) {
		const container = useRef<HTMLDivElement>(null);
		const widgetId = useRef<string | null>(null);
		const api = useRef<TurnstileApi | null>(null);
		const callbacks = useRef({ onToken, onUnavailable });
		callbacks.current = { onToken, onUnavailable };
		const [failed, setFailed] = useState(false);

		useImperativeHandle(ref, () => ({
			reset: () => {
				callbacks.current.onToken(null);
				if (api.current && widgetId.current) api.current.reset(widgetId.current);
			},
		}));

		useEffect(() => {
			let cancelled = false;
			loadTurnstile()
				.then((turnstile) => {
					if (cancelled || !container.current) return;
					api.current = turnstile;
					widgetId.current = turnstile.render(container.current, {
						sitekey: siteKey,
						action,
						theme: "auto",
						size: "flexible",
						"refresh-expired": "auto",
						callback: (token) => {
							setFailed(false);
							callbacks.current.onUnavailable?.(false);
							callbacks.current.onToken(token);
						},
						"expired-callback": () => callbacks.current.onToken(null),
						"timeout-callback": () => callbacks.current.onToken(null),
						"error-callback": () => {
							callbacks.current.onToken(null);
							callbacks.current.onUnavailable?.(true);
						},
					});
				})
				.catch(() => {
					if (cancelled) return;
					setFailed(true);
					callbacks.current.onUnavailable?.(true);
				});
			return () => {
				cancelled = true;
				if (api.current && widgetId.current) {
					api.current.remove(widgetId.current);
				}
				widgetId.current = null;
			};
		}, [siteKey, action]);

		return (
			<div
				ref={container}
				data-testid="human-check"
				className="min-h-[65px] w-full"
				aria-hidden={failed ? true : undefined}
			/>
		);
	}
);
