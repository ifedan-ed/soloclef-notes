import { z } from "zod";
import { baseApiService } from "@/lib/apis/base-api.service";
import {
	type AccountUpdate,
	type AdjustBody,
	adminModelListSchema,
	adminOverviewSchema,
	adminPackSchema,
	adminPlanSchema,
	adminPriceSchema,
	adminUserListSchema,
	ledgerSchema,
	type PackBody,
	type PlanBody,
	type PriceBody,
	reportSchema,
	type SettingsUpdate,
	settingsDescribeSchema,
} from "./billing-admin-schemas";
import { accountSchema, okSchema } from "./billing-schemas";

const BASE = "/api/v1/billing/admin";

/** Billing administration. Every route requires a billing administrator. */
export const billingAdminApi = {
	overview: (signal?: AbortSignal) =>
		baseApiService.get(`${BASE}/overview`, adminOverviewSchema, { signal }),
	saveSettings: (body: SettingsUpdate) =>
		baseApiService.put(`${BASE}/settings`, settingsDescribeSchema, { body }),
	createPlan: (body: PlanBody) => baseApiService.post(`${BASE}/plans`, adminPlanSchema, { body }),
	updatePlan: (id: number, body: PlanBody) =>
		baseApiService.put(`${BASE}/plans/${id}`, adminPlanSchema, { body }),
	updatePrice: (action: string, body: PriceBody) =>
		baseApiService.put(`${BASE}/prices/${encodeURIComponent(action)}`, adminPriceSchema, { body }),
	resetPrices: () => baseApiService.post(`${BASE}/reset-prices`, okSchema),
	createPack: (body: PackBody) => baseApiService.post(`${BASE}/packs`, adminPackSchema, { body }),
	updatePack: (id: number, body: PackBody) =>
		baseApiService.put(`${BASE}/packs/${id}`, adminPackSchema, { body }),
	deactivatePack: (id: number) => baseApiService.delete(`${BASE}/packs/${id}`, okSchema),
	users: (query: { q?: string; limit: number; offset: number }, signal?: AbortSignal) => {
		const params = new URLSearchParams({
			limit: String(query.limit),
			offset: String(query.offset),
		});
		if (query.q) params.set("q", query.q);
		return baseApiService.get(`${BASE}/users?${params}`, adminUserListSchema, { signal });
	},
	updateUser: (userId: string, body: AccountUpdate) =>
		baseApiService.put(`${BASE}/users/${userId}`, accountSchema, { body }),
	adjust: (userId: string, body: AdjustBody) =>
		baseApiService.post(
			`${BASE}/users/${userId}/adjust`,
			z.object({ plan_credits: z.number(), topup_credits: z.number() }),
			{ body }
		),
	ledger: (userId: string, signal?: AbortSignal) =>
		baseApiService.get(`${BASE}/users/${userId}/ledger`, ledgerSchema, { signal }),
	models: (signal?: AbortSignal) =>
		baseApiService.get(`${BASE}/models`, adminModelListSchema, { signal }),
	setModelTier: (modelId: string, tier: string) =>
		baseApiService.put(
			`${BASE}/models/${modelId.split("/").map(encodeURIComponent).join("/")}`,
			z.object({ model_id: z.string(), tier: z.string() }),
			{ body: { tier } }
		),
	report: (days: number, signal?: AbortSignal) =>
		baseApiService.get(`${BASE}/report?days=${days}`, reportSchema, { signal }),
};
