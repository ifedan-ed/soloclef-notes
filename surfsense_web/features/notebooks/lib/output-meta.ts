/** "8:07" for 487 seconds; hours are shown when needed. */
export function formatDuration(totalSeconds: number): string {
	const seconds = Math.max(0, Math.round(totalSeconds));
	const hours = Math.floor(seconds / 3600);
	const minutes = Math.floor((seconds % 3600) / 60);
	const rest = String(seconds % 60).padStart(2, "0");
	return hours > 0 ? `${hours}:${String(minutes).padStart(2, "0")}:${rest}` : `${minutes}:${rest}`;
}

export function isInFlight(status: string): boolean {
	return status === "queued" || status === "running";
}

/** Poll quickly while something is generating, not at all otherwise. */
export function outputsRefetchInterval(statuses: string[]): number | false {
	return statuses.some(isInFlight) ? 4000 : false;
}
