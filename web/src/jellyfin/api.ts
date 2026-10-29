import { apiRequest } from "@/common/api";

export interface JellyfinAuthResult {
	authenticated: boolean;
	url: string;
	serverName: string;
	userName: string;
	token: string;
	deviceId: string;
}

export interface JellyfinQuickConnect {
	url: string;
	code: string;
	secret: string;
	deviceId: string;
}

export async function authenticateByPassword(url: string, username: string, password: string) {
	return await apiRequest<JellyfinAuthResult>("POST", "jellyfin/auth/password", { url, username, password });
}

export async function initiateQuickConnect(url: string) {
	return await apiRequest<JellyfinQuickConnect>("POST", "jellyfin/auth/quick-connect", { url });
}

export async function pollQuickConnect(quickConnect: JellyfinQuickConnect) {
	return await apiRequest<JellyfinAuthResult>("POST", "jellyfin/auth/quick-connect/poll", { url: quickConnect.url, secret: quickConnect.secret, deviceId: quickConnect.deviceId });
}
