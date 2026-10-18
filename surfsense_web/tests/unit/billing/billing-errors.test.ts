import assert from "node:assert/strict";
import test from "node:test";
import {
	billingReturnTarget,
	billingSettingsPath,
	isBillingRefusal,
	returnFlag,
} from "@/features/billing/lib/billing-errors";
import {
	ANY_CRYPTO_METHOD,
	continuePaymentUrl,
	isCryptoBusy,
	isOpenPayment,
	methodMessageKey,
	payCurrencyFor,
} from "@/features/billing/lib/crypto-payment";
import { AppError } from "@/lib/error";

test("a 402 or a billing code is a billing refusal; other errors are not", () => {
	assert.equal(isBillingRefusal(new AppError("no credits", 402, "Payment Required")), true);
	assert.equal(
		isBillingRefusal(new AppError("x", 400, "Bad", "PLAN_STUDIO_KIND_NOT_INCLUDED")),
		true
	);
	assert.equal(
		isBillingRefusal(Object.assign(new Error("x"), { errorCode: "INSUFFICIENT_CREDITS" })),
		true
	);
	assert.equal(isBillingRefusal(new AppError("boom", 500, "Server Error", "SERVER_ERROR")), false);
	assert.equal(isBillingRefusal(null), false);
	assert.equal(isBillingRefusal("INSUFFICIENT_CREDITS"), false);
});

test("payment returns keep only a known flag and land on the workspace billing tab", () => {
	assert.equal(returnFlag("?checkout=success&session_id=cs_1"), "success");
	assert.equal(returnFlag("?checkout=cancel"), "cancel");
	assert.equal(returnFlag(new URLSearchParams("crypto=done")), "crypto_done");
	assert.equal(returnFlag("?checkout=evil"), null);
	assert.equal(returnFlag("?crypto=maybe"), null);
	assert.equal(returnFlag(""), null);
	assert.equal(billingSettingsPath(4), "/dashboard/4/user-settings/billing");
	assert.equal(
		billingReturnTarget(4, "success"),
		"/dashboard/4/user-settings/billing?checkout=success"
	);
	assert.equal(
		billingReturnTarget(4, "crypto_done"),
		"/dashboard/4/user-settings/billing?crypto=done"
	);
	assert.equal(billingReturnTarget("9", null), "/dashboard/9/user-settings/billing");
});

test("an open crypto payment can be continued on its BTCPay checkout", () => {
	const crypto = { payment_url: "https://pay.example.com/i/abc" };
	assert.equal(isOpenPayment("pending"), true);
	assert.equal(isOpenPayment("partial"), true);
	assert.equal(
		continuePaymentUrl({ provider: "btcpay", status: "pending", crypto }),
		"https://pay.example.com/i/abc"
	);
	assert.equal(
		continuePaymentUrl({ provider: "btcpay", status: "partial", crypto }),
		"https://pay.example.com/i/abc"
	);
	for (const status of ["paid", "expired", "failed", "refunded"]) {
		assert.equal(isOpenPayment(status), false);
		assert.equal(continuePaymentUrl({ provider: "btcpay", status, crypto }), null);
	}
	assert.equal(continuePaymentUrl({ provider: "stripe", status: "pending", crypto }), null);
	assert.equal(continuePaymentUrl({ provider: "btcpay", status: "pending", crypto: null }), null);
});

test("method ids map to message keys and to the request field", () => {
	assert.equal(methodMessageKey("BTC-CHAIN"), "btc_chain");
	assert.equal(methodMessageKey("USDT-TRON"), "usdt_tron");
	assert.equal(payCurrencyFor(ANY_CRYPTO_METHOD), undefined);
	assert.equal(payCurrencyFor(""), undefined);
	assert.equal(payCurrencyFor("USDT-POLYGON"), "USDT-POLYGON");
});

test("a busy BTCPay is recognised by its code", () => {
	assert.equal(isCryptoBusy(new AppError("busy", 503, "Unavailable", "CRYPTO_BUSY")), true);
	assert.equal(isCryptoBusy(new AppError("off", 503, "Unavailable", "CRYPTO_PAYMENTS_OFF")), false);
	assert.equal(isCryptoBusy(null), false);
});
