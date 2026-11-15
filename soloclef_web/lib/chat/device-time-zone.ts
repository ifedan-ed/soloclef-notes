/**
 * The IANA time zone of this device, for the chat request's ``time_zone``:
 * the server puts it in the prompt so that an automation the chat drafts
 * runs at the person's 08:00, not the server's. Undefined when the browser
 * cannot say (the field is then left out and the assistant asks), never a
 * guessed zone: the server refuses a zone it does not know, and a wrong one
 * would be a schedule hours off.
 */
export function deviceTimeZone(): string | undefined {
	try {
		const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
		return typeof zone === "string" && zone.trim() ? zone : undefined;
	} catch {
		return undefined;
	}
}
