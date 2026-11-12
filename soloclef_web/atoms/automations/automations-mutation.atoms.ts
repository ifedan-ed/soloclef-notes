import { atomWithMutation } from "jotai-tanstack-query";
import { toast } from "sonner";
import type {
	AutomationCreateRequest,
	AutomationUpdateRequest,
	TriggerCreateRequest,
	TriggerUpdateRequest,
} from "@/contracts/types/automation.types";
import { automationsApiService } from "@/lib/apis/automations-api.service";
import {
	markAutomationDeleted,
	unmarkAutomationDeleted,
} from "@/lib/automations/deleted-automations";
import {
	trackAutomationCreateFailed,
	trackAutomationDeleteFailed,
	trackAutomationTriggerAddFailed,
	trackAutomationTriggerRemoveFailed,
	trackAutomationTriggerUpdateFailed,
	trackAutomationUpdateFailed,
} from "@/lib/posthog/events";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { queryClient } from "@/lib/query-client/client";

// Cache invalidation strategy:
// - Automation writes invalidate the account's list + the touched detail.
// - Trigger writes only invalidate the parent automation detail (triggers
//   come back inline in AutomationDetail).
// The list side invalidates the whole "automations"/"list" prefix because
// the list is keyed by (limit, offset) and this layer does not track which
// page is on screen.

function invalidateList() {
	queryClient.invalidateQueries({ queryKey: ["automations", "list"] });
}

function invalidateDetail(automationId: number) {
	queryClient.invalidateQueries({
		queryKey: cacheKeys.automations.detail(automationId),
	});
}

export const createAutomationMutationAtom = atomWithMutation(() => ({
	meta: { suppressGlobalErrorToast: true },
	mutationFn: async (request: AutomationCreateRequest) => {
		return automationsApiService.createAutomation(request);
	},
	onSuccess: () => {
		invalidateList();
		toast.success("Automation created");
		// automation_created is now emitted server-side (AutomationService.create).
	},
	onError: (error: Error) => {
		console.error("Error creating automation:", error);
		toast.error("Failed to create automation");
		trackAutomationCreateFailed({ error: error.message });
	},
}));

export const updateAutomationMutationAtom = atomWithMutation(() => ({
	meta: { suppressGlobalErrorToast: true },
	mutationFn: async (vars: { automationId: number; patch: AutomationUpdateRequest }) => {
		return automationsApiService.updateAutomation(vars.automationId, vars.patch);
	},
	onSuccess: (_automation, vars) => {
		invalidateDetail(vars.automationId);
		invalidateList();
		toast.success("Automation updated");
		// automation_updated / automation_status_changed are now emitted
		// server-side (AutomationService.update).
	},
	onError: (error: Error, vars) => {
		console.error("Error updating automation:", error);
		toast.error("Failed to update automation");
		trackAutomationUpdateFailed({
			automation_id: vars.automationId,
			error: error.message,
		});
	},
}));

export const deleteAutomationMutationAtom = atomWithMutation(() => ({
	meta: { suppressGlobalErrorToast: true },
	mutationFn: async (vars: { automationId: number }) => {
		// Marked first: a detail/runs refetch racing the delete must not toast.
		markAutomationDeleted(vars.automationId);
		try {
			await automationsApiService.deleteAutomation(vars.automationId);
		} catch (error) {
			unmarkAutomationDeleted(vars.automationId);
			throw error;
		}
		return vars;
	},
	onSuccess: (vars) => {
		invalidateList();
		// Drop (never refetch) the deleted automation's detail and runs.
		for (const kind of ["detail", "runs"] as const) {
			const queryKey = ["automations", kind, vars.automationId];
			void queryClient.cancelQueries({ queryKey });
			queryClient.removeQueries({ queryKey });
		}
		toast.success("Automation deleted");
		// automation_deleted is now emitted server-side (AutomationService.delete).
	},
	onError: (error: Error, vars) => {
		console.error("Error deleting automation:", error);
		toast.error("Failed to delete automation");
		trackAutomationDeleteFailed({
			automation_id: vars.automationId,
			error: error.message,
		});
	},
}));

export const addTriggerMutationAtom = atomWithMutation(() => ({
	meta: { suppressGlobalErrorToast: true },
	mutationFn: async (vars: { automationId: number; payload: TriggerCreateRequest }) => {
		return automationsApiService.addTrigger(vars.automationId, vars.payload);
	},
	onSuccess: (_trigger, vars) => {
		invalidateDetail(vars.automationId);
		toast.success("Trigger added");
		// automation_trigger_added is now emitted server-side (TriggerService.add).
	},
	onError: (error: Error, vars) => {
		console.error("Error adding trigger:", error);
		toast.error("Failed to add trigger");
		trackAutomationTriggerAddFailed({
			automation_id: vars.automationId,
			error: error.message,
		});
	},
}));

export const updateTriggerMutationAtom = atomWithMutation(() => ({
	meta: { suppressGlobalErrorToast: true },
	mutationFn: async (vars: {
		automationId: number;
		triggerId: number;
		patch: TriggerUpdateRequest;
	}) => {
		return automationsApiService.updateTrigger(vars.automationId, vars.triggerId, vars.patch);
	},
	onSuccess: (_, vars) => {
		invalidateDetail(vars.automationId);
		toast.success("Trigger updated");
		// automation_trigger_updated is now emitted server-side (TriggerService.update).
	},
	onError: (error: Error, vars) => {
		console.error("Error updating trigger:", error);
		toast.error("Failed to update trigger");
		trackAutomationTriggerUpdateFailed({
			automation_id: vars.automationId,
			trigger_id: vars.triggerId,
			error: error.message,
		});
	},
}));

export const removeTriggerMutationAtom = atomWithMutation(() => ({
	meta: { suppressGlobalErrorToast: true },
	mutationFn: async (vars: { automationId: number; triggerId: number }) => {
		await automationsApiService.removeTrigger(vars.automationId, vars.triggerId);
		return vars;
	},
	onSuccess: (vars) => {
		invalidateDetail(vars.automationId);
		toast.success("Trigger removed");
		// automation_trigger_removed is now emitted server-side (TriggerService.remove).
	},
	onError: (error: Error, vars) => {
		console.error("Error removing trigger:", error);
		toast.error("Failed to remove trigger");
		trackAutomationTriggerRemoveFailed({
			automation_id: vars.automationId,
			trigger_id: vars.triggerId,
			error: error.message,
		});
	},
}));
