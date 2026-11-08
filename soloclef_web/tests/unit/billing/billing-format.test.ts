import assert from "node:assert/strict";
import test from "node:test";
import {
	allowancePercent,
	centsToDollars,
	creditsToMicros,
	dollarsToCents,
	formatCountdown,
	formatCredits,
	formatDate,
	formatMoney,
	microsToCredits,
	parseNumber,
} from "@/features/billing/lib/format";

test("credits show up to two decimals in the reader's locale", () => {
	assert.equal(formatCredits(1500, "en"), "1,500");
	assert.equal(formatCredits(2.5, "en"), "2.5");
	assert.equal(formatCredits(0.125, "en"), "0.13");
	assert.equal(formatCredits(1500, "es"), "1500");
	assert.equal(formatCredits(15000, "de"), "15.000");
});

test("money comes from cents, whole amounts without decimals", () => {
	assert.equal(formatMoney(900, "usd", "en"), "$9");
	assert.equal(formatMoney(999, "usd", "en"), "$9.99");
	assert.equal(formatMoney(2400, "USD", "en"), "$24");
	// An unknown currency code still renders something readable.
	assert.match(formatMoney(150, "zzzz", "en"), /1\.50/);
});

test("dates render without a time; junk renders empty", () => {
	assert.equal(formatDate("2026-10-01T00:00:00Z", "en"), "Oct 1, 2026");
	assert.equal(formatDate(null, "en"), "");
	assert.equal(formatDate("not a date", "en"), "");
});

test("the allowance bar is clamped to 0-100", () => {
	assert.equal(allowancePercent(40, 100), 40);
	assert.equal(allowancePercent(150, 100), 100);
	assert.equal(allowancePercent(-5, 100), 0);
	assert.equal(allowancePercent(10, 0), 0);
	assert.equal(allowancePercent(1, 3), 33);
});

test("the countdown reads m:ss, and h:mm:ss past an hour", () => {
	assert.equal(formatCountdown(0), "0:00");
	assert.equal(formatCountdown(65), "1:05");
	assert.equal(formatCountdown(3725), "1:02:05");
	assert.equal(formatCountdown(-3), "0:00");
});

test("signup bonus credits convert to micros (1 credit = 10,000 micros)", () => {
	assert.equal(creditsToMicros(50), 500_000);
	assert.equal(microsToCredits(500_000), 50);
	assert.equal(creditsToMicros(0.5), 5_000);
});

test("form numbers parse leniently", () => {
	assert.equal(parseNumber("12"), 12);
	assert.equal(parseNumber(" 1,5 "), 1.5);
	assert.equal(parseNumber("abc", 7), 7);
	assert.equal(dollarsToCents("9.99"), 999);
	assert.equal(dollarsToCents("24"), 2400);
	assert.equal(centsToDollars(900), "9.00");
});
