"use client";

import { useSearchParams } from "next/navigation";
import { useEffect, useRef } from "react";
import { toast } from "sonner";
import { replaceAddressQuery } from "@/lib/routing/replace-query";
import { readOAuthReturn, withoutOAuthMarkers } from "../model/oauth-return";

/**
 * Back from an MCP server's OAuth sign-in (`?mcp_oauth=<outcome>`): say how
 * it went once, hand the server's id to the page (which opens it), and drop
 * the markers from the URL so a reload does not repeat the message.
 */
export function useMcpOAuthReturn(onReturn: (connectorId: number | null) => void) {
	const params = useSearchParams();
	const handled = useRef<string | null>(null);
	const marker = params.toString();

	useEffect(() => {
		const returned = readOAuthReturn(new URLSearchParams(marker));
		if (!returned || handled.current === marker) return;
		handled.current = marker;
		if (returned.tone === "success") toast.success(returned.message);
		else toast.error(returned.message);
		onReturn(returned.connectorId);
		replaceAddressQuery(withoutOAuthMarkers(new URLSearchParams(marker)));
	}, [marker, onReturn]);
}
