/**
 * What the browser knows about time zones: the one it is set to, and the
 * IANA names it can list. Read only in the browser (the settings page is a
 * client page).
 */

/** The zone this browser is set to, such as "Africa/Lagos". */
export function browserTimeZone(): string {
	return Intl.DateTimeFormat().resolvedOptions().timeZone;
}

/**
 * Every IANA zone the browser can name. A browser too old to list them
 * (Intl.supportedValuesOf arrived in 2022) gives an empty list, and the
 * picker then offers the stored zone and the browser's own (timeZoneChoices).
 */
export function supportedTimeZones(): readonly string[] {
	return typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];
}

/** A zone's name as people read it: "America/New_York" as "America/New York". */
export function timeZoneLabel(zone: string): string {
	return zone.replaceAll("_", " ");
}
