import type { Schema } from "@/zero/schema/index";

/** Who is asking: every synced row is this account's own. */
export type Context =
	| {
			userId: string;
	  }
	| undefined;

declare module "@rocicorp/zero" {
	interface DefaultTypes {
		context: Context;
		schema: Schema;
	}
}
