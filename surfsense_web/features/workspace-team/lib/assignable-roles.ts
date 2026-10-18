import type { Role } from "@/contracts/types/roles.types";

/** Full access is ownership; nobody grants it through an invite or a role change. */
export function grantsFullAccess(role: Pick<Role, "permissions">): boolean {
	return role.permissions.includes("*");
}

const ORDER: Record<string, number> = { Editor: 0, Viewer: 1 };

/** Roles an invite or a role change may offer: never Owner, Editor before Viewer. */
export function assignableRoles<T extends Pick<Role, "name" | "permissions">>(roles: T[]): T[] {
	return roles
		.filter((role) => role.name !== "Owner" && !grantsFullAccess(role))
		.sort((a, b) => (ORDER[a.name] ?? 9) - (ORDER[b.name] ?? 9) || a.name.localeCompare(b.name));
}

/** The workspace's default role if it is assignable, else Viewer, else the first offered. */
export function defaultInviteRole<T extends Pick<Role, "name" | "permissions" | "is_default">>(
	roles: T[]
): T | undefined {
	const offered = assignableRoles(roles);
	return (
		offered.find((role) => role.is_default) ??
		offered.find((role) => role.name === "Viewer") ??
		offered[0]
	);
}
