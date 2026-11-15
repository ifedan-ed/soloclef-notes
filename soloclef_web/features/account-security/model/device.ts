import { z } from "zod";

/**
 * One place the account is signed in, as GET /auth/devices lists it
 * (soloclef_api/internal/signin, devices.go). A device is one sign-in: a
 * browser's, or the phone app's.
 */
export const deviceSchema = z.object({
	session_id: z.string(),
	/** "web" for a browser, "app" for the phone app. */
	kind: z.string(),
	/** The phone's own name ("Ada's iPhone"); a browser has none. */
	device_label: z.string().nullable(),
	signed_in_at: z.string(),
	last_used_at: z.string(),
	expires_at: z.string(),
	/**
	 * Whether this is the sign-in the list was asked from. The website's
	 * session is in cookies its script cannot read, so the server says which
	 * row is this browser.
	 */
	current: z.boolean(),
});

export const deviceListSchema = z.array(deviceSchema);

export type Device = z.infer<typeof deviceSchema>;
