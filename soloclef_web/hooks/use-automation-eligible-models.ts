"use client";

import { useAtomValue } from "jotai";
import { useMemo } from "react";
import {
	globalModelConnectionsAtom,
	modelConnectionsAtom,
	modelRolesAtom,
} from "@/atoms/model-connections/model-connections-query.atoms";
import {
	isPremiumModel,
	usableConnections,
} from "@/components/settings/model-connections/model-utils";
import type { ConnectionRead, ModelRead } from "@/contracts/types/model-connections.types";

/**
 * A single model the user may pick for an automation slot.
 */
export interface EligibleModelOption {
	id: number;
	name: string;
	/** Underlying model identifier (e.g. `gpt-4o`); shown as secondary text. */
	modelName: string;
	provider: string;
	/** `true` for user BYOK configs (positive ids), `false` for the shared models. */
	isBYOK: boolean;
	/** A premium-tier model: billed at the premium rate, shown with a "Premium" tag. */
	premium: boolean;
	/** Auto (id `0`, chat only): each run picks a standard model. */
	isAuto?: boolean;
}

/** Auto, as the backend names it: ``chat_model_id == 0``. */
export const AUTO_CHAT_MODEL_ID = 0;

const AUTO_OPTION: EligibleModelOption = {
	id: AUTO_CHAT_MODEL_ID,
	name: "Auto",
	modelName: "auto",
	provider: "auto",
	isBYOK: false,
	premium: false,
	isAuto: true,
};

export interface EligibleModelKind {
	options: EligibleModelOption[];
	/** Default selection: the workspace pref when eligible, else first option. */
	defaultId: number | null;
	/** O(1) id → option lookup for trigger labels (avoids per-render `.find()`). */
	byId: Map<number, EligibleModelOption>;
}

export interface AutomationEligibleModels {
	llm: EligibleModelKind;
	image: EligibleModelKind;
	vision: EligibleModelKind;
	isLoading: boolean;
}

/**
 * Build the option list for one model kind: the shared (global) models
 * followed by any of the user's own models. Every model may run an
 * automation (the backend no longer restricts them; see model_policy.py).
 */
function buildKind(
	globals: ConnectionRead[] | undefined,
	byok: ConnectionRead[] | undefined,
	capability: "chat" | "image_gen" | "vision",
	prefId: number | null | undefined
): EligibleModelKind {
	const supportsCapability = (model: ModelRead) => {
		if (capability === "chat") return Boolean(model.supports_chat);
		if (capability === "vision") return Boolean(model.supports_image_input);
		return Boolean(model.supports_image_generation);
	};
	const toOption = (connection: ConnectionRead, model: ModelRead, isBYOK: boolean) => ({
		id: model.id,
		name: model.display_name || model.model_id,
		modelName: model.model_id,
		provider: connection.provider,
		isBYOK,
		premium: isPremiumModel(model),
	});

	const sharedGlobals: EligibleModelOption[] = (globals ?? []).flatMap((connection) =>
		connection.models
			.filter((model) => model.enabled && supportsCapability(model))
			.map((model) => toOption(connection, model, false))
	);

	const byokOptions: EligibleModelOption[] = usableConnections(byok ?? []).flatMap((connection) =>
		connection.models
			.filter((model) => model.enabled && supportsCapability(model))
			.map((model) => toOption(connection, model, true))
	);

	const concrete = [...sharedGlobals, ...byokOptions];
	// Chat offers Auto first (a run resolves it to a standard model), and it is
	// the default unless the workspace's chat role names a model.
	const withAuto = capability === "chat" && concrete.length > 0;
	const options = withAuto ? [AUTO_OPTION, ...concrete] : concrete;
	const byId = new Map<number, EligibleModelOption>(options.map((o) => [o.id, o]));

	let defaultId: number | null = null;
	if (prefId != null && byId.has(prefId)) {
		defaultId = prefId;
	} else if (withAuto) {
		defaultId = AUTO_CHAT_MODEL_ID;
	} else if (options.length > 0) {
		defaultId = options[0].id;
	}

	return { options, defaultId, byId };
}

/**
 * Lists the LLM / image / vision models an automation can run on (the shared
 * models plus the user's own, and Auto for chat, which each run resolves to a
 * standard model), with a default selection seeded from the workspace's role
 * preferences.
 *
 * Everything is derived during render from the connection/model query atoms;
 * there are no effects, so option lists/maps keep stable references.
 */
export function useAutomationEligibleModels(): AutomationEligibleModels {
	const { data: byokConnections, isLoading: byokLoading } = useAtomValue(modelConnectionsAtom);
	const { data: globalConnections, isLoading: globalLoading } = useAtomValue(
		globalModelConnectionsAtom
	);
	const { data: roles, isLoading: rolesLoading } = useAtomValue(modelRolesAtom);

	const llm = useMemo(
		() => buildKind(globalConnections, byokConnections, "chat", roles?.chat_model_id),
		[globalConnections, byokConnections, roles?.chat_model_id]
	);

	const image = useMemo(
		() => buildKind(globalConnections, byokConnections, "image_gen", roles?.image_gen_model_id),
		[globalConnections, byokConnections, roles?.image_gen_model_id]
	);

	const vision = useMemo(
		() => buildKind(globalConnections, byokConnections, "vision", roles?.vision_model_id),
		[globalConnections, byokConnections, roles?.vision_model_id]
	);

	const isLoading = byokLoading || globalLoading || rolesLoading;

	return useMemo(() => ({ llm, image, vision, isLoading }), [llm, image, vision, isLoading]);
}
