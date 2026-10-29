import { type JellyfinAuthResult } from "@/jellyfin/api";
import { createContext, useContext } from "react";

export type JellyfinAuthOnAdd = (result: JellyfinAuthResult) => void;
export type JellyfinAuth = (onAdd: JellyfinAuthOnAdd) => void;
export const JellyfinAuthContext = createContext<JellyfinAuth | null>(null);
export function useJellyfinAuth() {
	const jellyfinAuth = useContext(JellyfinAuthContext);
	if (!jellyfinAuth) {
		throw new Error("useJellyfinAuth must be used within a JellyfinAuthProvider");
	}
	return jellyfinAuth;
}
