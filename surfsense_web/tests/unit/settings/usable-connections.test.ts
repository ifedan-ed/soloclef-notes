import assert from "node:assert/strict";
import test from "node:test";

import { usableConnections } from "@/components/settings/model-connections/model-utils";

// Run with: pnpm exec tsx --test tests/unit/settings/usable-connections.test.ts

test("a switched-off connection offers none of its models", () => {
	const connections = [{ id: 1, enabled: false }, { id: 2, enabled: true }, { id: 3 }];
	assert.deepEqual(
		usableConnections(connections).map((c) => c.id),
		[2, 3]
	);
});
