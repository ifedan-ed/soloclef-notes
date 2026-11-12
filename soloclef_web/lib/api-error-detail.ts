import { AppError } from "@/lib/error";

const GENERIC = new Set(["Something went wrong", "Failed to parse response"]);

/**
 * What the server said went wrong, when it said something a person can act on
 * ("That name is taken", "This file is too large"); otherwise the
 * caller's fallback.
 */
export function apiErrorDetail(error: unknown, fallback: string): string {
	if (error instanceof AppError && error.message && !GENERIC.has(error.message)) {
		return error.message;
	}
	return fallback;
}
