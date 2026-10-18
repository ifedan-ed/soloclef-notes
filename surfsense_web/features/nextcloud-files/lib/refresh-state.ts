import { type ImportErrorKind, importErrorKind } from "./import-errors";

/** Where one "Refresh from Nextcloud" click stands, as shown in the menu item. */
export type RefreshState =
	| { phase: "idle" }
	| { phase: "pending" }
	| { phase: "updated" }
	| { phase: "unchanged" }
	| { phase: "error"; kind: ImportErrorKind };

/** "queued" means the file changed and is being read again. */
export function refreshOutcome(status: "unchanged" | "queued"): RefreshState {
	return status === "queued" ? { phase: "updated" } : { phase: "unchanged" };
}

export function refreshFailure(error: unknown): RefreshState {
	return { phase: "error", kind: importErrorKind(error) };
}

/** The nextcloud_files message that labels the item in this state. */
export function refreshLabelKey(state: RefreshState): string {
	switch (state.phase) {
		case "idle":
			return "refresh";
		case "pending":
			return "refresh_pending";
		case "updated":
			return "refresh_updated";
		case "unchanged":
			return "refresh_unchanged";
		case "error":
			return state.kind === "auth"
				? "refresh_error_auth"
				: state.kind === "not_found"
					? "refresh_error_not_found"
					: "refresh_error";
	}
}
