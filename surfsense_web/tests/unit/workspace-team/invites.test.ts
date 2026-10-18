import assert from "node:assert/strict";
import test from "node:test";

import type { Invite } from "@/contracts/types/invites.types";
import { apiErrorDetail } from "@/features/workspace-team/lib/api-error-detail";
import { assignableRoles, defaultInviteRole } from "@/features/workspace-team/lib/assignable-roles";
import { looksLikeEmail } from "@/features/workspace-team/lib/email-address";
import { inviteExpiresAt } from "@/features/workspace-team/lib/invite-expiry";
import { inviteLink } from "@/features/workspace-team/lib/invite-link";
import { expiredInvites, pendingInvites } from "@/features/workspace-team/lib/pending-invites";
import { AppError, AuthorizationError } from "@/lib/error";

// Run with: pnpm exec tsx --test tests/unit/workspace-team/invites.test.ts

const role = (id: number, name: string, permissions: string[], is_default = false) => ({
	id,
	name,
	permissions,
	is_default,
});
const ROLES = [
	role(1, "Owner", ["*"]),
	role(2, "Viewer", ["documents:read"], true),
	role(3, "Editor", ["documents:read", "documents:create"]),
];

test("invites never offer Owner or any full-access role, Editor first", () => {
	const offered = assignableRoles([...ROLES, role(4, "Admins", ["*"])]);
	assert.deepEqual(
		offered.map((r) => r.name),
		["Editor", "Viewer"]
	);
});

test("the default role is the workspace default, else Viewer", () => {
	assert.equal(defaultInviteRole(ROLES)?.name, "Viewer");
	const noDefault = ROLES.map((r) => ({ ...r, is_default: false }));
	assert.equal(defaultInviteRole(noDefault)?.name, "Viewer");
	assert.equal(defaultInviteRole([role(1, "Owner", ["*"], true)]), undefined);
});

test("expiry is 7 or 30 days from now, or never", () => {
	const now = new Date("2026-09-25T12:00:00Z");
	assert.equal(inviteExpiresAt("7", now), "2026-10-02T12:00:00.000Z");
	assert.equal(inviteExpiresAt("30", now), "2026-10-25T12:00:00.000Z");
	assert.equal(inviteExpiresAt("never", now), null);
});

const invite = (id: number, status: Invite["status"], created_at: string): Invite => ({
	id,
	invite_code: `code-${id}`,
	workspace_id: 1,
	created_by_id: null,
	role_id: 2,
	expires_at: null,
	max_uses: 1,
	uses_count: 0,
	is_active: true,
	created_at,
	status,
});

test("pending and expired lists keep only their status, newest first", () => {
	const all = [
		invite(1, "pending", "2026-09-01T00:00:00Z"),
		invite(2, "used_up", "2026-09-02T00:00:00Z"),
		invite(3, "pending", "2026-09-03T00:00:00Z"),
		invite(4, "expired", "2026-09-04T00:00:00Z"),
		invite(5, "revoked", "2026-09-05T00:00:00Z"),
	];
	assert.deepEqual(
		pendingInvites(all).map((i) => i.id),
		[3, 1]
	);
	assert.deepEqual(
		expiredInvites(all).map((i) => i.id),
		[4]
	);
});

test("the link is the invite page on this origin", () => {
	assert.equal(
		inviteLink("https://notes.example.com/", "a_b-c"),
		"https://notes.example.com/invite/a_b-c"
	);
});

test("the server's reason is shown; a generic failure falls back", () => {
	const noAccount = new AppError("There is no account for x@y.z. Sign-ups are closed…", 422);
	assert.equal(apiErrorDetail(noAccount, "fallback"), noAccount.message);
	assert.equal(
		apiErrorDetail(new AuthorizationError("This invite is for another account.", 403), "f"),
		"This invite is for another account."
	);
	assert.equal(apiErrorDetail(new AppError("Something went wrong", 500), "fallback"), "fallback");
	assert.equal(apiErrorDetail(new Error("boom"), "fallback"), "fallback");
});

test("an address needs a local part, an @ and a dotted domain", () => {
	assert.equal(looksLikeEmail(" you@example.com "), true);
	assert.equal(looksLikeEmail("operator@example"), false);
	assert.equal(looksLikeEmail("not an email"), false);
});
