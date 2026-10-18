import { devices } from "@playwright/test";

/** Desktop project viewport. */
export const desktopDevice = {
	...devices["Desktop Chrome"],
	viewport: { width: 1280, height: 800 },
};

/** iPhone 14-ish on Chromium: 390×844, touch, mobile viewport meta honoured. */
export const phoneDevice = {
	...devices["Desktop Chrome"],
	viewport: { width: 390, height: 844 },
	deviceScaleFactor: 3,
	isMobile: true,
	hasTouch: true,
	userAgent: devices["iPhone 14"].userAgent,
};
