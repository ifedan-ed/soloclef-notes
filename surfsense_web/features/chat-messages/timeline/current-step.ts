import type { ActivityData } from "@/lib/chat/activity-journal";
import { getToolActivityId, getTraceLeafKind, type TracePartLike } from "./grouping";

/**
 * The step a steps segment's one line names (2026-10-10: one line that
 * changes as the answer works, instead of a list that grows a row per step,
 * for every kind of answer). Each new step replaces the one before it on
 * that line; the segment's steps are listed only when the line is opened.
 *
 * - `reasoning`: the model's reasoning, while it is being written and is the
 *   segment's last step.
 * - `activity`: otherwise the segment's latest tool step (its title and its
 *   icon), which is what the line has always said.
 * - null: nothing to name; the line says its phase or summary instead.
 */
export type CurrentTraceStep = { kind: "reasoning" } | { kind: "activity"; activity: ActivityData };

export function currentTraceStep({
	parts,
	indices,
	activities,
	firstActivityIndices,
	showReasoning,
	threadRunning,
	isPartRunning,
}: {
	parts: readonly TracePartLike[];
	indices: readonly number[];
	activities: ReadonlyMap<string, ActivityData>;
	firstActivityIndices: ReadonlyMap<string, number>;
	showReasoning: boolean;
	threadRunning: boolean;
	/** The part at this index is still being streamed. */
	isPartRunning: (index: number) => boolean;
}): CurrentTraceStep | null {
	for (let at = indices.length - 1; at >= 0; at -= 1) {
		const index = indices[at];
		const kind = getTraceLeafKind({
			part: parts[index],
			index,
			activities,
			firstActivityIndices,
			showReasoning,
		});
		if (kind === null) continue;
		if (kind === "reasoning" && threadRunning && isPartRunning(index)) {
			return { kind: "reasoning" };
		}
		break;
	}
	for (let at = indices.length - 1; at >= 0; at -= 1) {
		const activityId = getToolActivityId(parts[indices[at]]);
		const activity = activityId ? activities.get(activityId) : undefined;
		if (activity) return { kind: "activity", activity };
	}
	return null;
}
