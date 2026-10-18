import { AppError } from "@/lib/error";

export type ImportErrorKind = "auth" | "too_many" | "not_found" | "other";

/** Which message to show for a failed browse or import. */
export function importErrorKind(error: unknown): ImportErrorKind {
	if (!(error instanceof AppError)) return "other";
	if (error.code === "nextcloud_auth" || error.code === "nextcloud_reconnect") return "auth";
	if (error.code === "too_many_files") return "too_many";
	if (error.status === 404) return "not_found";
	return "other";
}
