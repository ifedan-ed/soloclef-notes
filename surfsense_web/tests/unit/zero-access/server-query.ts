// The Zero queries as zero-cache receives them, for access tests: built by
// the query route's own handler (app/api/zero/query/route.ts), in server
// column names, then evaluated against a few rows to see which they let
// through. Not a test file itself (tests/unit/**/*.test.ts are).
import assert from "node:assert/strict";

import { mustGetQuery } from "@rocicorp/zero";
import { handleQueryRequest } from "@rocicorp/zero/server";

import type { Context } from "@/types/zero";
import { queries } from "@/zero/queries";
import { schema } from "@/zero/schema";

export type Row = Record<string, unknown>;
type Side = { type: string; name?: string; value?: unknown };
export type Condition = {
	type: string;
	op?: string;
	left?: Side;
	right?: Side;
	conditions?: Condition[];
	related?: {
		correlation: { parentField: string[]; childField: string[] };
		subquery: { table: string; where?: Condition };
	};
};

function value(side: Side | undefined, row: Row): unknown {
	if (!side) return undefined;
	if (side.type === "column") return row[side.name as string];
	return side.value;
}

/** Whether `row` passes `cond`; `tables` holds the rows a subquery may reach. */
export function holds(
	cond: Condition | undefined,
	row: Row,
	tables: Record<string, Row[]>
): boolean {
	if (!cond) return true;
	switch (cond.type) {
		case "and":
			return (cond.conditions ?? []).every((c) => holds(c, row, tables));
		case "or":
			return (cond.conditions ?? []).some((c) => holds(c, row, tables));
		case "simple": {
			const left = value(cond.left, row);
			const right = value(cond.right, row);
			if (cond.op === "=") return left === right && left !== null && left !== undefined;
			if (cond.op === "IS") return left === right || (left == null && right == null);
			if (cond.op === "IS NOT") return !(left === right || (left == null && right == null));
			if (cond.op === "!=") return left !== right;
			throw new Error(`operator ${cond.op} not modelled`);
		}
		case "correlatedSubquery": {
			const related = cond.related;
			if (!related) return false;
			const [parentField] = related.correlation.parentField;
			const [childField] = related.correlation.childField;
			const children = (tables[related.subquery.table] ?? []).filter(
				(child) =>
					row[parentField] != null &&
					child[childField] === row[parentField] &&
					holds(related.subquery.where, child, tables)
			);
			return cond.op === "NOT EXISTS" ? children.length === 0 : children.length > 0;
		}
		default:
			throw new Error(`condition ${cond.type} not modelled`);
	}
}

/** The AST zero-cache gets for the named query, as the query route builds it. */
export async function serverAst(
	name: string,
	args: Record<string, unknown>,
	ctx: Context
): Promise<{ table: string; where?: Condition }> {
	const request = new Request("http://frontend/api/zero/query", {
		method: "POST",
		body: JSON.stringify(["transform", [{ id: "q1", name, args: [args] }]]),
	});
	const result = (await handleQueryRequest({
		handler: (queryName, queryArgs) => {
			const query = mustGetQuery(queries, queryName);
			return query.fn({ args: queryArgs, ctx });
		},
		schema,
		request,
		userID: ctx?.userId ?? "",
	})) as unknown as { kind: string; queries: { ast: { table: string; where?: Condition } }[] };
	assert.equal(result.kind, "QueryResponse");
	return result.queries[0].ast;
}
