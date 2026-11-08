/** Who may create and revoke public links, from the caller's workspace access. */
export interface WorkspaceAccess {
	is_owner: boolean;
	permissions?: string[];
}

function holds(access: WorkspaceAccess | null | undefined, permission: string): boolean {
	if (!access) return false;
	if (access.is_owner) return true;
	return access.permissions?.includes(permission) ?? false;
}

/** Members who may see public links (for a notebook's "Shared" badge and the links page). */
export function canViewPublicLinks(access: WorkspaceAccess | null | undefined): boolean {
	return holds(access, "public_sharing:view");
}

export function canCreatePublicLinks(access: WorkspaceAccess | null | undefined): boolean {
	return holds(access, "public_sharing:create");
}

/** Owners (public_sharing:delete) revoke any link; anyone revokes the links they made. */
export function canRevokePublicLink(
	access: WorkspaceAccess | null | undefined,
	currentUserId: string | null | undefined,
	createdByUserId: string | null | undefined
): boolean {
	if (holds(access, "public_sharing:delete")) return true;
	return !!currentUserId && !!createdByUserId && currentUserId === createdByUserId;
}
