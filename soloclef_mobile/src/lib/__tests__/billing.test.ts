/**
 * @jest-environment node
 */
import { ApiError, NetworkError } from "../api/errors";
import { billingAction, billingActionFor, billingActionLabelKey, billingUrl, formatCredits } from "../billing";
import { StreamError } from "../chat/stream-state";
import { hasMessage, SUPPORTED_LOCALES } from "../i18n/translate";

describe("billingAction", () => {
	it("links credit refusals to credits", () => {
		expect(billingAction("INSUFFICIENT_CREDITS", 402)).toBe("credits");
		expect(billingAction("INSUFFICIENT_CREDITS")).toBe("credits");
		expect(billingAction("PREMIUM_QUOTA_EXHAUSTED")).toBe("credits");
	});

	it("links plan refusals to plans", () => {
		for (const code of [
			"PLAN_PARSER_NOT_INCLUDED",
			"PLAN_VISION_NOT_INCLUDED",
			"PLAN_MODEL_TIER_NOT_INCLUDED",
			"PLAN_STUDIO_KIND_NOT_INCLUDED",
			"PLAN_VIDEO_TOO_LONG",
			"PLAN_TOO_MANY_PAGES",
			"PLAN_TOO_MANY_JOBS",
		]) {
			expect(billingAction(code, 402)).toBe("plans");
		}
	});

	it("treats any other 402 as a credits refusal", () => {
		expect(billingAction(undefined, 402)).toBe("credits");
		expect(billingAction("HTTP_ERROR", 402)).toBe("credits");
	});

	it("ignores everything else", () => {
		expect(billingAction(undefined, 500)).toBeNull();
		expect(billingAction("TURN_CANCELLING", 409)).toBeNull();
		expect(billingAction("RATE_LIMITED")).toBeNull();
		expect(billingAction(null, null)).toBeNull();
	});
});

describe("billingActionFor", () => {
	it("reads ApiError codes and statuses", () => {
		expect(billingActionFor(new ApiError("no", 402, "INSUFFICIENT_CREDITS"))).toBe("credits");
		expect(billingActionFor(new ApiError("no", 402, "PLAN_TOO_MANY_PAGES"))).toBe("plans");
		expect(billingActionFor(new ApiError("no", 402))).toBe("credits");
		expect(billingActionFor(new ApiError("no", 400, "BAD"))).toBeNull();
	});

	it("reads chat stream error codes", () => {
		expect(billingActionFor(new StreamError("no", "INSUFFICIENT_CREDITS"))).toBe("credits");
		expect(billingActionFor(new StreamError("no", "PLAN_MODEL_TIER_NOT_INCLUDED"))).toBe("plans");
		expect(billingActionFor(new StreamError("no", "PREMIUM_QUOTA_EXHAUSTED"))).toBe("credits");
		expect(billingActionFor(new StreamError("no"))).toBeNull();
	});

	it("ignores other failures", () => {
		expect(billingActionFor(new NetworkError())).toBeNull();
		expect(billingActionFor(new Error("x"))).toBeNull();
		expect(billingActionFor(null)).toBeNull();
	});
});

describe("billing link", () => {
	it("labels each action with a translated string", () => {
		expect(billingActionLabelKey("credits")).toBe("mobile.get_credits");
		expect(billingActionLabelKey("plans")).toBe("mobile.see_plans");
		for (const locale of SUPPORTED_LOCALES) {
			expect(hasMessage(locale, "mobile.get_credits")).toBe(true);
			expect(hasMessage(locale, "mobile.see_plans")).toBe(true);
		}
	});

	it("points at the server's /billing page", () => {
		expect(billingUrl("https://notes.example.com")).toBe("https://notes.example.com/billing");
		expect(billingUrl("https://notes.example.com/")).toBe("https://notes.example.com/billing");
	});

	it("formats credits with at most two decimals", () => {
		expect(formatCredits(100)).toBe("100");
		expect(formatCredits(42.5)).toBe("42.5");
		expect(formatCredits(1234.567)).toBe("1,234.57");
	});
});
