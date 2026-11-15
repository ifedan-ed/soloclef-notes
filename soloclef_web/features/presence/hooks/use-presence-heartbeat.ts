"use client";

import { useEffect } from "react";
import { sendPresence } from "../api/send-presence";
import { presenceClientId } from "../lib/presence-client-id";
import { startPresenceHeartbeat } from "../lib/presence-heartbeat";

/**
 * While a signed-in page is open and on screen, the server knows this tab is
 * here and sends no phone push. Mounted once, around every dashboard page
 * (app/dashboard/client-layout.tsx), so moving between pages keeps one beat.
 */
export function usePresenceHeartbeat() {
	useEffect(() => {
		const clientId = presenceClientId();
		return startPresenceHeartbeat({
			doc: document,
			win: window,
			send: (active) => sendPresence(clientId, active),
		});
	}, []);
}
