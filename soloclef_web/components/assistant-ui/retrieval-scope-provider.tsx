"use client";

import { getDefaultStore, useAtomValue } from "jotai";
import {
	createContext,
	type ReactNode,
	useCallback,
	useContext,
	useEffect,
	useRef,
	useState,
} from "react";
import { retrievalScopeAtom } from "@/atoms/chat/retrieval-scope.atom";
import { currentUserAtom } from "@/atoms/user/user-query.atoms";
import type { RetrievalScope } from "@/contracts/types/retrieval-scope.types";
import {
	persistRetrievalScopeCookie,
	readRetrievalScopeCookie,
} from "@/lib/chat/retrieval-scope-preferences";

type RetrievalScopeContextValue = {
	scope: RetrievalScope;
	setScope: (scope: RetrievalScope) => void;
};

const RetrievalScopeContext = createContext<RetrievalScopeContextValue | null>(null);
const jotaiStore = getDefaultStore();

export function RetrievalScopeProvider({ children }: { children: ReactNode }) {
	const { data: currentUser } = useAtomValue(currentUserAtom);
	// The saved choice is the account's, and the cookie says whose it is, so
	// it is read once the account is known: at once when it already is (the
	// usual case, the shell has asked for it), else when it arrives, unless
	// the person has picked a scope in the meantime.
	const [scope, setScopeState] = useState(() => readRetrievalScopeCookie(currentUser?.id));
	const restored = useRef(Boolean(currentUser?.id));
	const pendingPersistence = useRef(false);

	useEffect(() => {
		if (restored.current || !currentUser?.id) return;
		restored.current = true;
		setScopeState(readRetrievalScopeCookie(currentUser.id));
	}, [currentUser?.id]);

	useEffect(() => {
		jotaiStore.set(retrievalScopeAtom, scope);
	}, [scope]);

	useEffect(() => {
		if (!currentUser?.id || !pendingPersistence.current) return;
		pendingPersistence.current = false;
		persistRetrievalScopeCookie(currentUser.id, scope);
	}, [currentUser?.id, scope]);

	const setScope = useCallback(
		(nextScope: RetrievalScope) => {
			restored.current = true;
			setScopeState(nextScope);
			jotaiStore.set(retrievalScopeAtom, nextScope);
			pendingPersistence.current = !currentUser?.id;
			persistRetrievalScopeCookie(currentUser?.id, nextScope);
		},
		[currentUser?.id]
	);

	return (
		<RetrievalScopeContext.Provider value={{ scope, setScope }}>
			{children}
		</RetrievalScopeContext.Provider>
	);
}

export function useRetrievalScope(): RetrievalScopeContextValue {
	const value = useContext(RetrievalScopeContext);
	if (!value) throw new Error("useRetrievalScope must be used within RetrievalScopeProvider");
	return value;
}
