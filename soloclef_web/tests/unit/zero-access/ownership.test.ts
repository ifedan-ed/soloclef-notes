import assert from "node:assert/strict";
import test from "node:test";

import type { Context } from "@/types/zero";
import { zeroContextFromAccount } from "@/lib/zero/zero-context";
import { holds, type Row, serverAst } from "./server-query";

// Every Zero query as zero-cache receives it (server-query.ts), evaluated
// against rows of two accounts: each account syncs its own rows and none of
// the other's, whatever arguments its client sends.

const ME = "11111111-1111-1111-1111-111111111111";
const OTHER = "22222222-2222-2222-2222-222222222222";
const me: Context = { userId: ME };
const other: Context = { userId: OTHER };

// Rows 1 and 2 are ME's, 3 is OTHER's.
const owned = (extra: (id: number) => Row = () => ({})): Row[] => [
	{ id: 1, user_id: ME, ...extra(1) },
	{ id: 2, user_id: ME, ...extra(2) },
	{ id: 3, user_id: OTHER, ...extra(3) },
];
const threads = owned();
const automations = owned();
const tables = { new_chat_threads: threads, automations };

async function passing(
	name: string,
	args: Record<string, unknown> | undefined,
	ctx: Context,
	rows: Row[]
) {
	const ast = await serverAst(name, args, ctx);
	return rows.filter((row) => holds(ast.where, row, tables)).map((row) => row.id as number);
}

for (const name of ["documents.mine", "folders.mine", "connectors.mine", "notifications.mine"]) {
	test(`${name}: the caller's rows and no one else's`, async () => {
		const rows = owned();
		assert.deepEqual(await passing(name, undefined, me, rows), [1, 2]);
		assert.deepEqual(await passing(name, undefined, other, rows), [3]);
		assert.deepEqual(await passing(name, undefined, undefined, rows), []);
	});
}

for (const { name, idArg } of [
	{ name: "podcastRuns.byId", idArg: "podcastId" },
	{ name: "deliverableJobs.byId", idArg: "jobId" },
]) {
	test(`${name}: an output opens for its owner only, with or without a thread`, async () => {
		// Output 1 has no thread (a notebook's audio overview): still only ME's.
		const rows = owned((id) => ({ thread_id: id === 1 ? null : id }));
		for (const row of rows) {
			const mine = await passing(name, { [idArg]: row.id }, me, [row]);
			const theirs = await passing(name, { [idArg]: row.id }, other, [row]);
			assert.deepEqual(mine.length === 1, row.user_id === ME, `row ${row.id} for ME`);
			assert.deepEqual(theirs.length === 1, row.user_id === OTHER, `row ${row.id} for OTHER`);
		}
	});
}

for (const name of ["messages.byThread", "chatSession.byThread"]) {
	test(`${name}: a thread's rows follow the thread's owner`, async () => {
		const rows = threads.map((thread) => ({
			id: 100 + (thread.id as number),
			thread_id: thread.id,
		}));
		const readable = async (ctx: Context) => {
			const out: number[] = [];
			for (const row of rows) {
				if ((await passing(name, { threadId: row.thread_id }, ctx, [row])).length) {
					out.push(row.thread_id as number);
				}
			}
			return out;
		};
		assert.deepEqual(await readable(me), [1, 2]);
		assert.deepEqual(await readable(other), [3]);
		assert.deepEqual(await readable(undefined), []);
	});
}

test("automationRuns.byAutomation: a run follows its automation's owner", async () => {
	const runs = automations.map((a) => ({ id: 200 + (a.id as number), automation_id: a.id }));
	for (const run of runs) {
		const args = { automationId: run.automation_id };
		const ownerIsMe = automations.find((a) => a.id === run.automation_id)?.user_id === ME;
		assert.equal(
			(await passing("automationRuns.byAutomation", args, me, [run])).length === 1,
			ownerIsMe
		);
		assert.equal(
			(await passing("automationRuns.byAutomation", args, other, [run])).length === 1,
			!ownerIsMe
		);
	}
});

test("the context is the account's id from /users/me, and nothing else", () => {
	assert.deepEqual(zeroContextFromAccount({ id: ME, email: "a@b.c" }), { userId: ME });
	assert.equal(zeroContextFromAccount({ id: "" }), null);
	assert.equal(zeroContextFromAccount({}), null);
	assert.equal(zeroContextFromAccount(null), null);
});
