"use client";

import { BUILD_TIME_DEPLOYMENT_MODE } from "@/lib/env-config";

/**
 * Whether this is a self-hosted deployment. It is the build's
 * NEXT_PUBLIC_DEPLOYMENT_MODE: the static site has no server to read a
 * runtime DEPLOYMENT_MODE from, and every Soloclef server is self-hosted.
 */
export function useIsSelfHosted() {
	return BUILD_TIME_DEPLOYMENT_MODE === "self-hosted";
}
