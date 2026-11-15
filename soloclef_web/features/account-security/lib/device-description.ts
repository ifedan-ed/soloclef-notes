import type { Device } from "../model/device";

/** What to call a device when it has no name of its own, as a key in `account_security`. */
export type UnnamedDevice = "unnamed_browser" | "unnamed_phone";

/**
 * A device's name: the phone's own label, or what kind of sign-in it is.
 * A browser never has a label; a phone always sends one, but an old sign-in
 * may lack it.
 */
export function deviceName(device: Pick<Device, "device_label" | "kind">): {
	label: string | null;
	unnamed: UnnamedDevice;
} {
	const label = device.device_label?.trim() || null;
	return { label, unnamed: device.kind === "app" ? "unnamed_phone" : "unnamed_browser" };
}

/**
 * Whether the row says when the sign-in ends. A phone's end moves with each
 * use (180 days unused) and is worth stating; a browser's is at most 30 days
 * off and the line is long enough without it. The phone app shows the same.
 */
export function showsEnd(device: Pick<Device, "kind">): boolean {
	return device.kind === "app";
}

/** The rows in the order to show them: this browser first, then by latest use. */
export function orderDevices(devices: readonly Device[]): Device[] {
	return [...devices].sort((a, b) => {
		if (a.current !== b.current) return a.current ? -1 : 1;
		return Date.parse(b.last_used_at) - Date.parse(a.last_used_at);
	});
}
