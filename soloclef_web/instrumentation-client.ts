import posthog from "posthog-js";

/**
 * PostHog initialisation for the web app. Every event is marked
 * platform "web", so the web's events stay apart from the phone app's in the
 * same project.
 */
const PLATFORM = "web";

async function initPostHog() {
	try {
		if (!process.env.NEXT_PUBLIC_POSTHOG_KEY) return;

		posthog.init(process.env.NEXT_PUBLIC_POSTHOG_KEY, {
			api_host: "https://assets.surfsense.com",
			ui_host: "https://us.posthog.com",
			defaults: "2026-01-30",
			capture_pageview: "history_change",
			capture_pageleave: true,
			before_send: (event) => {
				if (event?.properties) {
					event.properties.platform = PLATFORM;

					const params = new URLSearchParams(window.location.search);
					const ref = params.get("ref");
					if (ref) {
						event.properties.ref_code = ref;
						event.properties.$set = {
							...event.properties.$set,
							initial_ref_code: ref,
						};
						event.properties.$set_once = {
							...event.properties.$set_once,
							first_ref_code: ref,
						};
					}

					event.properties.$set = {
						...event.properties.$set,
						platform: PLATFORM,
						last_seen_at: new Date().toISOString(),
					};

					event.properties.$set_once = {
						...event.properties.$set_once,
						first_seen_platform: PLATFORM,
					};
				}
				return event;
			},
			loaded: () => {
				if (typeof window !== "undefined") {
					// `loaded` hands back a `PostHogInterface`, but it's the same
					// singleton as the default import (typed `PostHog`); use that to
					// keep `window.posthog` correctly typed.
					window.posthog = posthog;
				}
			},
		});
	} catch {
		// PostHog init failed (likely ad-blocker) – app must continue to work
	}
}

if (typeof window !== "undefined") {
	window.posthog = posthog;

	if ("requestIdleCallback" in window) {
		requestIdleCallback(() => {
			void initPostHog();
		});
	} else {
		setTimeout(() => {
			void initPostHog();
		}, 3500);
	}
}
