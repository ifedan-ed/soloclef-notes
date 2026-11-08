/** Why a public link did not open, as the public page tells it. */
export type PublicLinkFailure = "expired" | "gone" | "error";

export function publicLinkFailure(error: unknown): PublicLinkFailure {
	const status =
		typeof error === "object" && error !== null && "status" in error
			? (error as { status?: unknown }).status
			: undefined;
	if (status === 410) return "expired";
	if (status === 404) return "gone";
	return "error";
}
