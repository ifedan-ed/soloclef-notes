"use client";

import {
	useConnectionState,
	useZero,
	ZeroProvider as ZeroReactProvider,
} from "@rocicorp/zero/react";
import { usePathname } from "next/navigation";
import { useEffect, useMemo, useRef, useState } from "react";
import { useSession } from "@/hooks/use-session";
import { authenticatedFetch } from "@/lib/auth-fetch";
import { handleUnauthorized, isPublicRoute, refreshSession } from "@/lib/auth-utils";
import { buildBackendUrl } from "@/lib/env-config";
import { ZERO_CONTEXT_PATH, zeroContextFromAccount } from "@/lib/zero/zero-context";
import type { Context } from "@/types/zero";
import { queries } from "@/zero/queries";
import { schema } from "@/zero/schema";

const configuredCacheURL = process.env.NEXT_PUBLIC_ZERO_CACHE_URL;
type ZeroContext = Exclude<Context, undefined>;
type ZeroContextState = ZeroContext | null | undefined;

function getCacheURL() {
	if (configuredCacheURL) return configuredCacheURL;
	if (typeof window !== "undefined") {
		return `${window.location.origin}/zero`;
	}
	return "http://localhost:4848";
}

async function fetchZeroContext(): Promise<ZeroContext | null> {
	const response = await authenticatedFetch(buildBackendUrl(ZERO_CONTEXT_PATH), {
		skipAuthRedirect: true,
	});
	if (response.status === 401) {
		// Auth is dead (refresh already failed inside authenticatedFetch). This
		// provider gates the whole app tree, so nothing below it (e.g.
		// DashboardShell) can run its own redirect — do it here.
		handleUnauthorized();
		return null;
	}
	if (!response.ok) return null;
	return zeroContextFromAccount(await response.json().catch(() => null));
}

// Cap how many times we will refresh the session in response to Zero's
// `needs-auth` state before giving up. Without this, a persistent auth failure
// in zero-cache makes the connection cycle needs-auth -> connecting -> needs-auth
// indefinitely, each cycle firing a `/auth/jwt/refresh` and quickly tripping the
// backend rate limiter (HTTP 429).
const MAX_ZERO_AUTH_REFRESH_ATTEMPTS = 3;
const ZERO_AUTH_REFRESH_BASE_DELAY_MS = 1_000;
const ZERO_AUTH_REFRESH_MAX_DELAY_MS = 30_000;

// ZeroAuthSync refreshes the session cookie when zero-cache says the
// connection needs auth, then reconnects: the cookie is what zero-cache
// forwards, so the connection carries no token of its own.
function ZeroAuthSync() {
	const zero = useZero();
	const connectionState = useConnectionState();
	const refreshAttemptsRef = useRef(0);
	const refreshInFlightRef = useRef(false);

	// Once a connection is established, clear the backoff so future
	// auth expirations get a fresh set of refresh attempts.
	useEffect(() => {
		if (connectionState.name === "connected") {
			refreshAttemptsRef.current = 0;
		}
	}, [connectionState.name]);

	useEffect(() => {
		if (connectionState.name !== "needs-auth") return;
		if (refreshInFlightRef.current) return;

		if (refreshAttemptsRef.current >= MAX_ZERO_AUTH_REFRESH_ATTEMPTS) {
			handleUnauthorized();
			return;
		}

		const attempt = refreshAttemptsRef.current;
		const delayMs =
			attempt === 0
				? 0
				: Math.min(
						ZERO_AUTH_REFRESH_BASE_DELAY_MS * 2 ** (attempt - 1),
						ZERO_AUTH_REFRESH_MAX_DELAY_MS
					);

		refreshInFlightRef.current = true;
		const timer = setTimeout(() => {
			refreshAttemptsRef.current += 1;
			refreshSession()
				.then((refreshed) => {
					if (!refreshed) {
						handleUnauthorized();
						return;
					}

					zero.connection.connect();
				})
				.finally(() => {
					refreshInFlightRef.current = false;
				});
		}, delayMs);

		return () => clearTimeout(timer);
	}, [connectionState.name, zero]);

	return null;
}

function AuthenticatedZeroProvider({ children }: { children: React.ReactNode }) {
	const [context, setContext] = useState<ZeroContextState>(undefined);

	useEffect(() => {
		let isMounted = true;
		void fetchZeroContext().then((next) => {
			if (isMounted) setContext(next);
		});
		return () => {
			isMounted = false;
		};
	}, []);

	if (!context) return null;

	return (
		<ZeroClientProvider userID={context.userId} context={context}>
			{children}
		</ZeroClientProvider>
	);
}

function ZeroClientProvider({
	children,
	userID,
	context,
}: {
	children: React.ReactNode;
	userID: string;
	context: ZeroContext;
}) {
	const cacheURL = useMemo(() => getCacheURL(), []);
	const opts = useMemo(
		() => ({ userID, schema, queries, context, cacheURL }),
		[userID, context, cacheURL]
	);

	return (
		<ZeroReactProvider {...opts}>
			<ZeroAuthSync />
			{children}
		</ZeroReactProvider>
	);
}

function WebZeroProvider({ children }: { children: React.ReactNode }) {
	const session = useSession();

	// Same reasoning as fetchZeroContext: this provider blocks the whole tree,
	// so the login redirect must happen here, not in a child that never mounts.
	useEffect(() => {
		if (session.status === "unauthenticated") handleUnauthorized();
	}, [session.status]);

	if (session.status !== "authenticated") {
		return null;
	}

	return <AuthenticatedZeroProvider>{children}</AuthenticatedZeroProvider>;
}

export function ZeroProvider({ children }: { children: React.ReactNode }) {
	const pathname = usePathname();

	if (isPublicRoute(pathname)) {
		return <>{children}</>;
	}

	return <WebZeroProvider>{children}</WebZeroProvider>;
}
