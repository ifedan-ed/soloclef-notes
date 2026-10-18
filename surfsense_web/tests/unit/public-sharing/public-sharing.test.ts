import assert from "node:assert/strict";
import test from "node:test";

import {
	canCreatePublicLinks,
	canRevokePublicLink,
} from "@/features/public-sharing/lib/public-link-access";
import { publicLinkFailure } from "@/features/public-sharing/lib/public-link-failure";
import { publicFilePath, publicLinkUrl } from "@/features/public-sharing/lib/public-link-url";
import {
	DEFAULT_EXPIRY,
	EXPIRY_CHOICES,
	expiryPayload,
	isExpiryChoice,
} from "@/features/public-sharing/model/share-expiry";
import { AppError, NotFoundError } from "@/lib/error";

// Run with: pnpm exec tsx --test tests/unit/public-sharing/public-sharing.test.ts

test("a new link lasts seven days unless the owner picks thirty or never", () => {
	assert.equal(DEFAULT_EXPIRY, "7");
	assert.deepEqual(expiryPayload("7"), { expires_in_days: 7 });
	assert.deepEqual(expiryPayload("30"), { expires_in_days: 30 });
	assert.deepEqual(expiryPayload("never"), { expires_in_days: null });
	assert.deepEqual([...EXPIRY_CHOICES], ["7", "30", "never"]);
	assert.equal(isExpiryChoice("30"), true);
	assert.equal(isExpiryChoice("3"), false);
});

test("only owners (or a role granted it) see the share actions", () => {
	assert.equal(canCreatePublicLinks({ is_owner: true }), true);
	assert.equal(
		canCreatePublicLinks({ is_owner: false, permissions: ["public_sharing:view"] }),
		false
	);
	assert.equal(
		canCreatePublicLinks({ is_owner: false, permissions: ["public_sharing:create"] }),
		true
	);
	assert.equal(canCreatePublicLinks(null), false);
});

test("owners revoke any link; others only the links they made", () => {
	const editor = { is_owner: false, permissions: ["public_sharing:view"] };
	assert.equal(canRevokePublicLink({ is_owner: true }, "me", "someone"), true);
	assert.equal(canRevokePublicLink(editor, "me", "someone"), false);
	assert.equal(canRevokePublicLink(editor, "me", "me"), true);
	assert.equal(canRevokePublicLink(editor, undefined, null), false);
});

test("an expired link and a revoked one are told apart", () => {
	assert.equal(publicLinkFailure(new AppError("This link has expired", 410)), "expired");
	assert.equal(publicLinkFailure(new NotFoundError("Not found", 404)), "gone");
	assert.equal(publicLinkFailure(new Error("boom")), "error");
});

test("links and files point at the public routes", () => {
	assert.equal(publicLinkUrl("https://notes.example/", "tok"), "https://notes.example/public/tok");
	assert.equal(publicFilePath("tok", 5), "/api/v1/public/tok/artifacts/5/content");
});
