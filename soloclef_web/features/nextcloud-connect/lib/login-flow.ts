import { AppError } from "@/lib/error";

/** Where one "Connect Nextcloud" attempt stands. */
export type LoginFlowState =
	| { phase: "idle" }
	| { phase: "starting" }
	| { phase: "waiting"; flowId: string; loginUrl: string; expiresAt: number }
	| { phase: "connected" }
	| { phase: "expired" }
	| { phase: "failed"; reason: LoginFailure };

export type LoginFailure = "rate_limited" | "not_configured" | "unusable" | "other";

export const IDLE: LoginFlowState = { phase: "idle" };

/** Poll every 2 s while the person is likely signing in, then ease off. */
export function pollDelay(elapsedMs: number): number {
	return elapsedMs < 2 * 60_000 ? 2_000 : 5_000;
}

export function waitingFor(
	started: { flow_id: string; login_url: string; expires_in: number },
	now: number
): LoginFlowState {
	return {
		phase: "waiting",
		flowId: started.flow_id,
		loginUrl: started.login_url,
		expiresAt: now + started.expires_in * 1000,
	};
}

/** The state after a failed start or poll. A 404 poll means the flow is gone. */
export function afterError(error: unknown): LoginFlowState {
	if (!(error instanceof AppError)) return { phase: "failed", reason: "other" };
	if (error.code === "nextcloud_login_expired" || error.status === 404) {
		return { phase: "expired" };
	}
	if (error.status === 429) return { phase: "failed", reason: "rate_limited" };
	if (error.code === "nextcloud_not_configured" || error.status === 503) {
		return { phase: "failed", reason: "not_configured" };
	}
	if (error.code === "nextcloud_login_unusable") return { phase: "failed", reason: "unusable" };
	return { phase: "failed", reason: "other" };
}

/** Nextcloud forgets a login after 20 minutes; so do we. */
export function isPastExpiry(now: number, expiresAt: number): boolean {
	return now >= expiresAt;
}

/** The nextcloud_connect message that explains a finished or failed attempt. */
export function loginFlowMessageKey(state: LoginFlowState): string | null {
	switch (state.phase) {
		case "expired":
			return "login_expired";
		case "failed":
			return `login_failed_${state.reason}`;
		default:
			return null;
	}
}
