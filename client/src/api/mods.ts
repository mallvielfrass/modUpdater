import { call, upload } from "./http";
import type {
  AdminModsResponse,
  AdminReleases,
  ClientLatest,
  Mod,
  ModConfig,
  RestartResult,
  RevokedMod,
  ScanResult,
  ServerLogs,
} from "../types";

function normalizeMod(mod: Mod): Mod {
  return {
    ...mod,
    client_only: mod.client_only === true,
    client_required: mod.client_required === true,
    revoked: mod.revoked === true,
    configs: Array.isArray(mod.configs) ? mod.configs : [],
  };
}

export async function fetchPublicMods(serverUrl: string): Promise<{ mods: Mod[]; revoked: RevokedMod[] }> {
  const raw = await call<{ mods: Mod[]; revoked?: RevokedMod[] }>(serverUrl, "/api/mods", {});
  return { mods: (raw.mods ?? []).map(normalizeMod), revoked: raw.revoked ?? [] };
}

export async function fetchClientLatest(serverUrl: string): Promise<ClientLatest> {
  const raw = await call<Partial<ClientLatest>>(serverUrl, "/api/client/latest", {});
  return {
    version: raw.version ?? "",
    linux_sha256: raw.linux_sha256 ?? "",
    linux_size: raw.linux_size ?? 0,
    windows_sha256: raw.windows_sha256 ?? "",
    windows_size: raw.windows_size ?? 0,
  };
}

export async function listAdminMods(serverUrl: string, token: string): Promise<AdminModsResponse> {
  const raw = await call<AdminModsResponse>(serverUrl, "/api/admin/mods", { token });
  return {
    mods: (raw.mods ?? []).map(normalizeMod),
    last_restart_at: raw.last_restart_at ?? "",
    pending_restart: raw.pending_restart ?? [],
  };
}

export async function listReleases(serverUrl: string, token: string): Promise<AdminReleases> {
  const raw = await call<AdminReleases>(serverUrl, "/api/admin/releases", { token });
  return {
    active_version: raw.active_version ?? "",
    releases: raw.releases ?? [],
  };
}

export function releaseClientVersion(serverUrl: string, token: string, version: string): Promise<void> {
  return call(serverUrl, `/api/admin/releases/${encodeURIComponent(version)}/release`, { method: "POST", token });
}

export function deleteRelease(serverUrl: string, token: string, version: string): Promise<void> {
  return call(serverUrl, `/api/admin/releases/${encodeURIComponent(version)}`, { method: "DELETE", token });
}

export function scanAdminMods(serverUrl: string, token: string): Promise<ScanResult> {
  return call(serverUrl, "/api/admin/mods/scan", { method: "POST", token });
}

export function deleteMod(serverUrl: string, token: string, id: string): Promise<void> {
  return call(serverUrl, `/api/admin/mods/${id}`, { method: "DELETE", token });
}

export function revokeMod(serverUrl: string, token: string, id: string): Promise<{ mod: Mod }> {
  return call(serverUrl, `/api/admin/mods/${id}/revoke`, { method: "POST", token });
}

export function restoreMod(serverUrl: string, token: string, id: string): Promise<{ mod: Mod }> {
  return call(serverUrl, `/api/admin/mods/${id}/restore`, { method: "POST", token });
}

export interface ModrinthImport {
  added: number;
  updated: number;
  skipped: number;
  failed: string[];
  added_files?: string[];
  skipped_files?: string[];
}

export function importModrinth(serverUrl: string, token: string, file: File): Promise<ModrinthImport> {
  const body = new FormData();
  body.append("file", file, file.name);
  return upload<ModrinthImport>(serverUrl, "/api/admin/mods/modrinth", "POST", token, body);
}

export type ModFields = {
  name?: string;
  description?: string;
  client?: boolean;
  clientOnly?: boolean;
  clientRequired?: boolean;
  file?: File;
};

export function createMod(
  serverUrl: string,
  token: string,
  file: File,
  fields: { name?: string; description?: string; client: boolean; clientOnly?: boolean; clientRequired?: boolean },
  onProgress?: (loaded: number, total: number) => void,
): Promise<{ mod: Mod }> {
  return upload(serverUrl, "/api/admin/mods", "POST", token, formFrom({ ...fields, file }), onProgress);
}

export function updateMod(
  serverUrl: string,
  token: string,
  id: string,
  fields: ModFields,
  onProgress?: (loaded: number, total: number) => void,
): Promise<{ mod: Mod }> {
  const body = formFrom(fields);
  if (fields.file) return upload(serverUrl, `/api/admin/mods/${id}`, "PUT", token, body, onProgress);
  return call(serverUrl, `/api/admin/mods/${id}`, { method: "PUT", token, body });
}

function formFrom(fields: ModFields): FormData {
  const body = new FormData();
  if (fields.file) body.append("file", fields.file, fields.file.name);
  if (fields.name !== undefined) body.append("name", fields.name);
  if (fields.description !== undefined) body.append("description", fields.description);
  if (fields.client !== undefined) body.append("client", fields.client ? "true" : "false");
  if (fields.clientOnly !== undefined) body.append("client_only", fields.clientOnly ? "true" : "false");
  if (fields.clientRequired !== undefined) body.append("client_required", fields.clientRequired ? "true" : "false");
  return body;
}

export function uploadModConfig(
  serverUrl: string,
  token: string,
  modId: string,
  dest: string,
  kind: ModConfig["kind"],
  file: File,
  onProgress?: (loaded: number, total: number) => void,
): Promise<void> {
  const body = new FormData();
  body.append("dest", dest);
  body.append("kind", kind);
  body.append("file", file, file.name);
  return upload<unknown>(serverUrl, `/api/admin/mods/${modId}/configs`, "POST", token, body, onProgress).then(() => undefined);
}

export function deleteModConfig(serverUrl: string, token: string, modId: string, configId: string): Promise<void> {
  return call(serverUrl, `/api/admin/mods/${modId}/configs/${configId}`, { method: "DELETE", token });
}

export interface ChangelogDay {
  day: string;
  lines: string[];
}

export function listChangelog(serverUrl: string, token: string): Promise<{ days: ChangelogDay[] }> {
  return call<{ days?: ChangelogDay[] }>(serverUrl, "/api/admin/changelog", { token }).then((raw) => ({
    days: raw.days ?? [],
  }));
}

export function generateChangelog(serverUrl: string, token: string): Promise<{ days: ChangelogDay[] }> {
  return call<{ days?: ChangelogDay[] }>(serverUrl, "/api/admin/changelog", { method: "POST", token }).then((raw) => ({
    days: raw.days ?? [],
  }));
}

export async function restartServer(
  serverUrl: string,
  token: string,
  message: string,
): Promise<RestartResult> {
  const raw = await call<RestartResult>(serverUrl, "/api/admin/server/restart", {
    method: "POST",
    token,
    json: { message },
  });
  return {
    ...raw,
    last_restart_at: raw.last_restart_at ?? "",
    pending_restart: raw.pending_restart ?? [],
    backup_error: raw.backup_error ?? "",
  };
}

export type LogWindow = "hour" | "day" | "all";

const LOG_VIEW_LINES = 200;
export const LOG_DOWNLOAD_LINES = 5000;

export function serverLogs(
  serverUrl: string,
  token: string,
  since: LogWindow = "hour",
  lines = LOG_VIEW_LINES,
): Promise<ServerLogs> {
  const count = Math.min(LOG_DOWNLOAD_LINES, Math.max(1, Math.trunc(lines)));
  return call(serverUrl, `/api/admin/server/logs?lines=${count}&since=${since}`, { token });
}

export interface RconMod {
  id: string;
  version: string;
  file: string;
}

export interface RconStatus {
  up: boolean;
  online: number;
  max: number;
  names: string[];
  players_raw: string;
  players_ok: boolean;
  mods_ok: boolean;
  mods: RconMod[];
  mods_raw: string;
}

export function fetchRcon(serverUrl: string, token: string): Promise<RconStatus> {
  return call<RconStatus>(serverUrl, "/api/admin/server/rcon", { token }).then((raw) => ({
    up: !!raw.up,
    online: raw.online ?? 0,
    max: raw.max ?? 0,
    names: raw.names ?? [],
    players_raw: raw.players_raw ?? "",
    players_ok: !!raw.players_ok,
    mods_ok: !!raw.mods_ok,
    mods: raw.mods ?? [],
    mods_raw: raw.mods_raw ?? "",
  }));
}
