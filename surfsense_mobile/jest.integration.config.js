/**
 * Integration tests run in plain Node (real fetch, streams and FormData)
 * against a real backend; see src/lib/__tests__/backend.integration.test.ts.
 */
module.exports = {
	testEnvironment: "node",
	testMatch: ["**/*.integration.test.ts"],
	transform: { "\\.[jt]sx?$": ["babel-jest", { presets: ["babel-preset-expo"] }] },
	transformIgnorePatterns: ["/node_modules/(?!zod)"],
};
