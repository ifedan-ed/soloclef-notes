"use client";

import { type ReactNode, useCallback, useRef, useState } from "react";
import { useRuntimeConfig } from "@/components/providers/runtime-config";
import { TurnstileWidget, type TurnstileWidgetHandle } from "../ui/turnstile-widget";

export interface HumanCheck {
	/** False when the deployment has no Turnstile key: nothing to solve. */
	enabled: boolean;
	/** A token is in hand (or the check is off). */
	ready: boolean;
	/** The widget could not load or errored. */
	unavailable: boolean;
	/** The solved token to send with the protected request (null when off or unsolved). */
	token: string | null;
	/** Call after every protected request: a token is good for one use. */
	reset: () => void;
	widget: ReactNode;
}

export function useHumanCheck(action: string): HumanCheck {
	const { turnstileSiteKey } = useRuntimeConfig();
	const [token, setToken] = useState<string | null>(null);
	const [unavailable, setUnavailable] = useState(false);
	const handle = useRef<TurnstileWidgetHandle>(null);
	const enabled = Boolean(turnstileSiteKey);

	const reset = useCallback(() => {
		setToken(null);
		handle.current?.reset();
	}, []);

	return {
		enabled,
		ready: !enabled || Boolean(token),
		unavailable,
		token: enabled ? token : null,
		reset,
		widget: turnstileSiteKey ? (
			<TurnstileWidget
				ref={handle}
				siteKey={turnstileSiteKey}
				action={action}
				onToken={setToken}
				onUnavailable={setUnavailable}
			/>
		) : null,
	};
}
