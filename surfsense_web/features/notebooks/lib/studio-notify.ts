import type { StudioOutput } from "../model/notebook";
import { isInFlight } from "./output-meta";

export type NotifyPreference = "on" | "off" | null;

export const STARTED_OUTPUTS_KEY = "surfsense:studio-started-outputs";
const MAX_REMEMBERED = 50;

export function notifyPreferenceKey(userId: string): string {
	return `surfsense:studio-notify:${userId}`;
}

export function parseNotifyPreference(raw: string | null): NotifyPreference {
	return raw === "on" || raw === "off" ? raw : null;
}

/** Remember an output this browser started, newest last, bounded. */
export function rememberStarted(current: number[], outputId: number): number[] {
	return [...current.filter((id) => id !== outputId), outputId].slice(-MAX_REMEMBERED);
}

export function parseStarted(raw: string | null): number[] {
	try {
		const value = JSON.parse(raw ?? "[]");
		return Array.isArray(value) ? value.filter((id) => Number.isSafeInteger(id)) : [];
	} catch {
		return [];
	}
}

/**
 * Outputs the user started that have just finished: in flight in the
 * previous poll, ready or failed in this one.
 */
export function justFinished(
	previous: ReadonlyMap<number, string>,
	outputs: readonly StudioOutput[],
	started: readonly number[]
): StudioOutput[] {
	return outputs.filter(
		(output) =>
			started.includes(output.id) &&
			isInFlight(previous.get(output.id) ?? "") &&
			(output.status === "ready" || output.status === "failed")
	);
}

/** Whether to show the "Get notified" banner. */
export function shouldOfferNotifications(
	supported: boolean,
	permission: string | null,
	preference: NotifyPreference
): boolean {
	return supported && preference === null && permission !== "denied";
}
