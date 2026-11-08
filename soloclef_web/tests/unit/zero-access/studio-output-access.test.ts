import assert from "node:assert/strict";
import test from "node:test";

import type { Context } from "@/types/zero";
import { holds, type Row, serverAst } from "./server-query";

// A Studio output's live row (podcast, video, deliverable job) reached every
// member of its workspace through bySpace and byId, whatever thread made it:
// another member's private chat, or their Studio request (a private thread of
// its own). Now a row follows its thread's access rule (thread-access.ts);
// a row with no thread stays the workspace's.

const ME = "11111111-1111-1111-1111-111111111111";
const MEMBER = "22222222-2222-2222-2222-222222222222";

// Workspace 7 is ME's and MEMBER's (ME owns it); workspace 9 is not ME's.
const threads: Row[] = [
	{ id: 1, workspace_id: 7, visibility: "PRIVATE", created_by_id: ME },
	{ id: 2, workspace_id: 7, visibility: "PRIVATE", created_by_id: MEMBER },
	{ id: 3, workspace_id: 7, visibility: "SEARCH_SPACE", created_by_id: MEMBER },
	{ id: 4, workspace_id: 7, visibility: "PRIVATE", created_by_id: null },
	{ id: 6, workspace_id: 9, visibility: "SEARCH_SPACE", created_by_id: MEMBER },
];
const tables = { new_chat_threads: threads };

// Output id = 100 + its thread (or 100 for none), in workspace 7 unless 6's.
const outputs: Row[] = [
	{ id: 100, workspace_id: 7, thread_id: null },
	...threads.map((t) => ({
		id: 100 + (t.id as number),
		workspace_id: t.workspace_id,
		thread_id: t.id,
	})),
];

const QUERIES = [
	{ name: "podcastRuns", idArg: "podcastId" },
	{ name: "videoRuns", idArg: "runId" },
	{ name: "deliverableJobs", idArg: "jobId" },
];

async function listed(name: string, workspaceId: number, ctx: Context): Promise<number[]> {
	const ast = await serverAst(`${name}.bySpace`, { workspaceId }, ctx);
	return outputs
		.filter((row) => row.workspace_id === workspaceId && holds(ast.where, row, tables))
		.map((row) => row.id as number);
}

async function openable(name: string, idArg: string, ctx: Context): Promise<number[]> {
	const readable: number[] = [];
	for (const row of outputs) {
		const ast = await serverAst(`${name}.byId`, { [idArg]: row.id }, ctx);
		if (holds(ast.where, row, tables)) readable.push(row.id as number);
	}
	return readable;
}

const me: Context = { userId: ME, allowedSpaceIds: [7], ownedSpaceIds: [7] };
const member: Context = { userId: MEMBER, allowedSpaceIds: [7, 9], ownedSpaceIds: [] };

for (const { name, idArg } of QUERIES) {
	test(`${name}: another member's private chat's outputs reach nobody else`, async () => {
		// 102 is MEMBER's private chat's: not ME's. 104's thread has no creator
		// and ME owns workspace 7. 100 has no thread: the workspace's.
		assert.deepEqual(await listed(name, 7, me), [100, 101, 103, 104]);
		assert.deepEqual(await openable(name, idArg, me), [100, 101, 103, 104]);
		assert.deepEqual(await listed(name, 7, member), [100, 102, 103]);
		assert.deepEqual(await openable(name, idArg, member), [100, 102, 103, 106]);
	});

	test(`${name}: outside one's workspaces, or signed out, nothing`, async () => {
		assert.deepEqual(await listed(name, 9, me), []);
		assert.deepEqual(await openable(name, idArg, undefined), []);
	});
}
