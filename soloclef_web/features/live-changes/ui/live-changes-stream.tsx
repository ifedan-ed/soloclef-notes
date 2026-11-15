"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { handleUnauthorized } from "@/lib/auth-utils";
import { openChangeStream } from "../api/open-change-stream";
import { applyLiveTargets } from "../lib/apply-live-targets";
import { ChangeCoalescer } from "../lib/change-coalescer";
import { LiveChangesConnection } from "../lib/live-changes-connection";
import { everyLiveTarget, targetsForChange, uniqueTargets } from "../lib/live-query-targets";

/**
 * Keeps the signed-in app's cached reads current: one change stream per tab,
 * each change refreshing the reads it names. Mounted once, by the signed-in
 * shell (app/dashboard/dashboard-shell.tsx), after it has confirmed the
 * session; leaving the signed-in pages (signing out lands on /login)
 * unmounts it and closes the stream.
 */
export function LiveChangesStream() {
	const queryClient = useQueryClient();

	useEffect(() => {
		const coalescer = new ChangeCoalescer((changes) =>
			applyLiveTargets(queryClient, uniqueTargets(changes.flatMap(targetsForChange)))
		);
		const connection = new LiveChangesConnection({
			open: openChangeStream,
			onReady: (resumed) => {
				if (resumed) return; // the server replays what was missed
				applyLiveTargets(queryClient, everyLiveTarget(), {
					readBefore: Date.now(),
					keepInFlight: true,
				});
			},
			onChange: (change) => coalescer.add(change),
			onSignedOut: handleUnauthorized,
		});
		void connection.start();

		// Back to a tab: a change lost on the way (a sleeping laptop, a
		// network switch) must not leave it stale. The app's queries do not
		// refetch on focus by default (lib/query-client/client.ts); the live
		// ones do it here, once, together.
		const onVisibilityChange = () => {
			if (document.visibilityState !== "visible") return;
			applyLiveTargets(queryClient, everyLiveTarget(), { keepInFlight: true });
		};
		document.addEventListener("visibilitychange", onVisibilityChange);

		return () => {
			document.removeEventListener("visibilitychange", onVisibilityChange);
			connection.stop();
			coalescer.dispose();
		};
	}, [queryClient]);

	return null;
}
