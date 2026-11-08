import { createContext, useContext } from "react";

/**
 * The shown thread's reply is stopping: Stop was pressed (on this page or
 * another) and the backend is still saving the partial reply. The composer
 * shows "Stopping…" instead of Stop until the thread is idle.
 */
const TurnStoppingContext = createContext(false);

export const TurnStoppingProvider = TurnStoppingContext.Provider;

export function useTurnStopping(): boolean {
	return useContext(TurnStoppingContext);
}
