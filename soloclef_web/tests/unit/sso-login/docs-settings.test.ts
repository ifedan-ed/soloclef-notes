import assert from "node:assert/strict";
import test from "node:test";
import { createElement, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
	SsoAccountsFor,
	SsoButtonLabel,
	SsoDomains,
	SsoOnly,
	SsoProviderHost,
} from "@/components/docs/single-sign-on";
import { SiteAddress } from "@/components/docs/site-address";
import { type Brand, resolveBrand } from "@/lib/brand";
import brandJson from "@/lib/brand/brand.json";
import {
	DEFAULT_SSO_BUTTON_LABEL,
	formatDomains,
	parseDomains,
	readSsoSettings,
	SSO_SETTINGS,
	type SsoSettings,
} from "@/lib/sso-settings";

// Run with: pnpm exec tsx --test tests/unit/sso-login/docs-settings.test.ts

const html = (node: ReactNode) => renderToStaticMarkup(createElement("p", null, node));

const domainsOnly = readSsoSettings({
	enabled: "TRUE",
	buttonLabel: "Continue with Example SSO",
	discoveryUrl: "https://sso.example.com/application/o/notes/.well-known/openid-configuration",
	autocreateDomains: "example.com",
	allowSignup: "FALSE",
});

test("unset, there is no single sign-on and the default name", () => {
	const none = readSsoSettings({});
	assert.deepEqual(none, {
		enabled: false,
		buttonLabel: DEFAULT_SSO_BUTTON_LABEL,
		providerHost: null,
		accounts: "nobody",
		domains: [],
	});
	assert.equal(DEFAULT_SSO_BUTTON_LABEL, "Continue with single sign-on");
	// The build's own values; empty in a plain test run.
	assert.equal(
		SSO_SETTINGS.enabled,
		process.env.NEXT_PUBLIC_OIDC_ENABLED?.toUpperCase() === "TRUE"
	);
});

test("the docs read the server's own button, provider and account rule", () => {
	assert.equal(domainsOnly.enabled, true);
	assert.equal(domainsOnly.buttonLabel, "Continue with Example SSO");
	assert.equal(domainsOnly.providerHost, "sso.example.com");
	assert.equal(domainsOnly.accounts, "domains");
	assert.deepEqual(domainsOnly.domains, ["example.com"]);
	assert.equal(
		readSsoSettings({ allowSignup: "true", autocreateDomains: "a.org" }).accounts,
		"anyone"
	);
	assert.equal(readSsoSettings({ discoveryUrl: "not a url" }).providerHost, null);
});

test("domains are parsed like the backend's and listed in words", () => {
	assert.deepEqual(parseDomains(" @Example.org, b.net. b.net"), ["b.net", "example.org"]);
	assert.equal(formatDomains([]), "");
	assert.equal(formatDomains(["a.com"]), "@a.com");
	assert.equal(formatDomains(["a.com", "b.org"]), "@a.com or @b.org");
	assert.equal(formatDomains(["a.com", "b.org", "c.net"]), "@a.com, @b.org or @c.net");
});

test("docs components print this server's values", () => {
	const settings: SsoSettings = domainsOnly;
	assert.equal(
		html(createElement(SsoButtonLabel, { settings })),
		"<p>Continue with Example SSO</p>"
	);
	assert.equal(html(createElement(SsoProviderHost, { settings })), "<p> (sso.example.com)</p>");
	assert.equal(html(createElement(SsoDomains, { settings })), "<p>@example.com</p>");
	const none = readSsoSettings({});
	assert.equal(html(createElement(SsoProviderHost, { settings: none })), "<p></p>");
	assert.equal(html(createElement(SsoOnly, { settings: none }, "x")), "<p></p>");
	assert.equal(html(createElement(SsoOnly, { settings }, "x")), "<p>x</p>");
});

test("exactly one account sentence shows, and 'nobody' when there is no sign-on", () => {
	const shown = (settings: SsoSettings) =>
		(["anyone", "domains", "nobody"] as const).filter(
			(accounts) => html(createElement(SsoAccountsFor, { accounts, settings }, "x")) === "<p>x</p>"
		);
	assert.deepEqual(shown(domainsOnly), ["domains"]);
	assert.deepEqual(shown(readSsoSettings({ enabled: "TRUE", allowSignup: "TRUE" })), ["anyone"]);
	assert.deepEqual(shown(readSsoSettings({ enabled: "TRUE" })), ["nobody"]);
	assert.deepEqual(shown(readSsoSettings({ autocreateDomains: "example.com" })), ["nobody"]);
});

test("an address on this server appears only when it has one", () => {
	const unconfigured = brandJson as Brand;
	const brand = resolveBrand(unconfigured, { siteUrl: "https://notes.example.org/" });
	assert.equal(
		html(createElement(SiteAddress, { path: "/mcp", brand })),
		"<p> (<code>https://notes.example.org/mcp</code>)</p>"
	);
	assert.equal(html(createElement(SiteAddress, { path: "/mcp", brand: unconfigured })), "<p></p>");
});
