import { useEffect, useState } from "react";
import { settledAnnouncer } from "./settled-announcement";

/**
 * The text a polite status region next to an answer's one line holds: the
 * line's step once it has stayed on it a moment (settled-announcement.ts),
 * "" before that. `text` is "" while nothing is to be said. Rewriting the
 * region with the same text says nothing, so a step is said once.
 */
export function useSettledAnnouncement(text: string): string {
	const [said, setSaid] = useState("");
	const [announcer] = useState(() => settledAnnouncer(setSaid));
	useEffect(() => {
		announcer.update(text);
	}, [announcer, text]);
	useEffect(() => () => announcer.dispose(), [announcer]);
	return said;
}
