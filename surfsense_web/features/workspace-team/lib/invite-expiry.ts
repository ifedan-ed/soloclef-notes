/** How long an invite stays open. Seven days unless the inviter picks otherwise. */
export const INVITE_EXPIRY_CHOICES = ["7", "30", "never"] as const;
export type InviteExpiry = (typeof INVITE_EXPIRY_CHOICES)[number];
export const DEFAULT_INVITE_EXPIRY: InviteExpiry = "7";

const DAY_MS = 24 * 60 * 60 * 1000;

/** The ISO timestamp the API stores, or null for an invite that never expires. */
export function inviteExpiresAt(choice: InviteExpiry, now: Date = new Date()): string | null {
	if (choice === "never") return null;
	return new Date(now.getTime() + Number(choice) * DAY_MS).toISOString();
}
