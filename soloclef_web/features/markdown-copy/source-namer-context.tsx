"use client";

import { createContext, type FC, type ReactNode, useContext } from "react";
import { accountSourceNamer, type SourceNamer } from "./source-namer";

const SourceNamerContext = createContext<SourceNamer>(accountSourceNamer);

/**
 * The namer copies inside this tree use. Signed-in pages need none (the
 * account's namer is the default); a public page provides its own, built
 * from the sources it was served, so a visitor's copy asks no private route.
 */
export const SourceNamerProvider: FC<{ namer: SourceNamer; children: ReactNode }> = ({
	namer,
	children,
}) => <SourceNamerContext.Provider value={namer}>{children}</SourceNamerContext.Provider>;

export function useSourceNamer(): SourceNamer {
	return useContext(SourceNamerContext);
}
