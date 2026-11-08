import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { NextIntlClientProvider } from "next-intl";
import { createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
	afterError,
	isPastExpiry,
	loginFlowMessageKey,
	pollDelay,
	waitingFor,
} from "@/features/nextcloud-connect/lib/login-flow";
import { NextcloudLoginPanel } from "@/features/nextcloud-connect/ui/nextcloud-login-panel";
import { importErrorKind } from "@/features/nextcloud-files/lib/import-errors";
import { AppError, NotFoundError } from "@/lib/error";

const messages = JSON.parse(readFileSync(join(process.cwd(), "messages", "en.json"), "utf8"));

function render(element: ReactElement): string {
	return renderToStaticMarkup(
		createElement(NextIntlClientProvider, {
			locale: "en",
			messages,
			timeZone: "UTC",
			// biome-ignore lint/correctness/noChildrenProp: the provider's props type requires children.
			children: element,
		})
	);
}

const noop = () => undefined;

test("a started login waits on its flow until Nextcloud's 20 minutes are up", () => {
	const state = waitingFor(
		{ flow_id: "f1", login_url: "https://cloud.example.com/login/v2/flow/x", expires_in: 1200 },
		1_000
	);
	assert.equal(state.phase, "waiting");
	if (state.phase !== "waiting") return;
	assert.equal(state.flowId, "f1");
	assert.equal(state.expiresAt, 1_000 + 1_200_000);
	assert.equal(isPastExpiry(1_000 + 1_199_999, state.expiresAt), false);
	assert.equal(isPastExpiry(1_000 + 1_200_000, state.expiresAt), true);
});

test("polling is brisk while the person signs in, then eases off", () => {
	assert.equal(pollDelay(0), 2_000);
	assert.equal(pollDelay(119_000), 2_000);
	assert.equal(pollDelay(121_000), 5_000);
});

test("each failure says what happened", () => {
	assert.deepEqual(afterError(new NotFoundError("gone", 404)), { phase: "expired" });
	assert.deepEqual(afterError(new AppError("slow down", 429)), {
		phase: "failed",
		reason: "rate_limited",
	});
	assert.deepEqual(afterError(new AppError("x", 503, "", "nextcloud_not_configured")), {
		phase: "failed",
		reason: "not_configured",
	});
	assert.deepEqual(afterError(new AppError("x", 502, "", "nextcloud_login_unusable")), {
		phase: "failed",
		reason: "unusable",
	});
	assert.deepEqual(afterError(new Error("network")), { phase: "failed", reason: "other" });
	assert.equal(loginFlowMessageKey({ phase: "expired" }), "login_expired");
	assert.equal(
		loginFlowMessageKey({ phase: "failed", reason: "rate_limited" }),
		"login_failed_rate_limited"
	);
	assert.equal(loginFlowMessageKey({ phase: "idle" }), null);
});

test("every message a failure can show exists", () => {
	for (const reason of ["rate_limited", "not_configured", "unusable", "other"] as const) {
		const key = loginFlowMessageKey({ phase: "failed", reason });
		assert.ok(key && messages.nextcloud_connect[key], `missing ${key}`);
	}
});

test("the sign-in opens as a plain new-tab link to Nextcloud", () => {
	const html = render(
		createElement(NextcloudLoginPanel, {
			state: {
				phase: "waiting",
				flowId: "f1",
				loginUrl: "https://cloud.example.com/login/v2/flow/abc",
				expiresAt: 0,
			},
			server: "cloud.example.com",
			onCancel: noop,
			onRetry: noop,
		})
	);
	assert.match(html, /<a [^>]*href="https:\/\/cloud\.example\.com\/login\/v2\/flow\/abc"/);
	assert.match(html, /target="_blank"/);
	assert.match(html, /rel="noopener noreferrer"/);
	assert.match(html, /Sign in to cloud\.example\.com/);
	assert.doesNotMatch(html, /flow_id|poll/);
});

test("an expired sign-in offers to start again; an idle one shows nothing", () => {
	const expired = render(
		createElement(NextcloudLoginPanel, {
			state: { phase: "expired" },
			server: "cloud.example.com",
			onCancel: noop,
			onRetry: noop,
		})
	);
	assert.match(expired, /The sign-in expired/);
	assert.match(expired, /Try again/);
	const idle = render(
		createElement(NextcloudLoginPanel, {
			state: { phase: "idle" },
			server: "cloud.example.com",
			onCancel: noop,
			onRetry: noop,
		})
	);
	assert.equal(idle, "");
});

test("a picker call on a connection that needs renewing reads as an auth problem", () => {
	assert.equal(importErrorKind(new AppError("x", 400, "", "nextcloud_reconnect")), "auth");
});
