/** The page an invitee opens; the same link the invitation email carries. */
export function inviteLink(origin: string, inviteCode: string): string {
	return `${origin.replace(/\/$/, "")}/invite/${encodeURIComponent(inviteCode)}`;
}
