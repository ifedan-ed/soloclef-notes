import assert from "node:assert/strict";
import test from "node:test";

import type { Context } from "@/types/zero";
import { holds, type Row, serverAst } from "../zero-access/server-query";

// The chat queries as zero-cache receives them (zero-access/server-query.ts),
// evaluated against a few threads to see whose messages they let through.

const ME = "11111111-1111-1111-1111-111111111111";
const MEMBER = "22222222-2222-2222-2222-222222222222";

// Workspace 7 is shared by ME and MEMBER (ME owns it); workspace 8 is ME's
// but owned by MEMBER; workspace 9 is not ME's at all.
const threads: Row[] = [
	{ id: 1, workspace_id: 7, visibility: "PRIVATE", created_by_id: ME },
	{ id: 2, workspace_id: 7, visibility: "PRIVATE", created_by_id: MEMBER },
	{ id: 3, workspace_id: 7, visibility: "SEARCH_SPACE", created_by_id: MEMBER },
	{ id: 4, workspace_id: 7, visibility: "PRIVATE", created_by_id: null },
	{ id: 5, workspace_id: 8, visibility: "PRIVATE", created_by_id: null },
	{ id: 6, workspace_id: 9, visibility: "SEARCH_SPACE", created_by_id: MEMBER },
	{ id: 7, workspace_id: 8, visibility: "SEARCH_SPACE", created_by_id: MEMBER },
];
const tables: Record<string, Row[]> = { new_chat_threads: threads };

async function readableThreads(name: string, ctx: Context): Promise<number[]> {
	const readable: number[] = [];
	for (const thread of threads) {
		const ast = await serverAst(name, { threadId: thread.id as number }, ctx);
		// A message (or comment, or session row) of this thread.
		const row = { id: 100 + (thread.id as number), thread_id: thread.id };
		if (holds(ast.where, row, tables)) readable.push(thread.id as number);
	}
	return readable;
}

const me: Context = { userId: ME, allowedSpaceIds: [7, 8], ownedSpaceIds: [7] };

for (const name of ["messages.byThread", "comments.byThread", "chatSession.byThread"]) {
	test(`${name}: own threads, shared ones, and the owner's creatorless ones only`, async () => {
		// 2 is MEMBER's private thread: never. 5 has no creator but workspace 8
		// is MEMBER's: never. 6 is outside ME's workspaces: never.
		assert.deepEqual(await readableThreads(name, me), [1, 3, 4, 7]);
	});

	test(`${name}: the other member sees their own private thread, not ME's`, async () => {
		const member: Context = { userId: MEMBER, allowedSpaceIds: [7, 8, 9], ownedSpaceIds: [8] };
		assert.deepEqual(await readableThreads(name, member), [2, 3, 5, 6, 7]);
	});

	test(`${name}: an owned workspace the user no longer belongs to grants nothing`, async () => {
		const left: Context = { userId: ME, allowedSpaceIds: [8], ownedSpaceIds: [7] };
		assert.deepEqual(await readableThreads(name, left), [7]);
	});

	test(`${name}: no signed-in user reads nothing`, async () => {
		assert.deepEqual(await readableThreads(name, undefined), []);
	});
}
