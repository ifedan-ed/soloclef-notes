import type { Invite } from "@/contracts/types/invites.types";

function newestFirst(a: Invite, b: Invite): number {
	return b.created_at.localeCompare(a.created_at);
}

/** Invites still waiting for their address to accept. */
export function pendingInvites(invites: Invite[]): Invite[] {
	return invites.filter((invite) => invite.status === "pending").sort(newestFirst);
}

/** Invites that ran out before anyone accepted them. */
export function expiredInvites(invites: Invite[]): Invite[] {
	return invites.filter((invite) => invite.status === "expired").sort(newestFirst);
}
