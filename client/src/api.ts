import type {
  AdminModsResponse,
  AdminReleases,
  AdminUser,
  BackupRollback,
  S3Settings,
  ServerBackup,
  ClientLatest,
  Invite,
  LoginResult,
  Mod,
  ModConfig,
  RestartResult,
  RevokedMod,
  ScanResult,
  ServerLogs,
} from "./types";

declare const __MODUPDATER_SERVER_URL__: string;

export const SERVER_URL = __MODUPDATER_SERVER_URL__;

export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly detail: string;
  readonly log: string;

  constructor(code: string, status: number, detail = "", log = "") {
    super(code);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
    this.detail = detail;
    this.log = log;
  }
}

type UnauthorizedHandler = () => void | Promise<void>;

let onUnauthorized: UnauthorizedHandler | null = null;

export function setUnauthorizedHandler(handler: UnauthorizedHandler | null): void {
  onUnauthorized = handler;
}

export async function unauthorized(): Promise<void> {
  if (onUnauthorized) await onUnauthorized();
}

export function errorCodeFromBody(body: unknown): string {
  return errorParts(body).code;
}

function errorParts(body: unknown): { code: string; detail: string } {
  if (body && typeof body === "object" && "error" in body) {
    const obj = body as { error?: unknown; message?: unknown; detail?: unknown };
    const code = typeof obj.error === "string" && obj.error ? obj.error : "internal";
    const raw = typeof obj.detail === "string" ? obj.detail : typeof obj.message === "string" ? obj.message : "";
    return { code, detail: safeDetail(raw, code) };
  }
  return { code: "internal", detail: "" };
}

function safeDetail(raw: string, code: string): string {
  const text = raw.trim();
  if (!text || text === code || text.length > 180) return "";
  if (/[\r\n]/.test(text)) return "";
  if (/stack|goroutine|panic|traceback|\.go:\d+|\.tsx?:\d+|at\s+\S+\s+\(/i.test(text)) return "";
  return text;
}

export function errorCode(err: unknown): string {
  if (err instanceof ApiError) return err.code;
  if (typeof err === "string" && err) return err;
  if (err && typeof err === "object") {
    const message = (err as { message?: unknown }).message;
    if (typeof message === "string" && message) return message;
  }
  if (err instanceof Error && err.message) return err.message;
  return "internal";
}

const phrases: Record<string, string> = {
  unauthorized: "Сессия недействительна.",
  invalid_password: "Пароль не соответствует требованиям.",
  invalid_name: "Некорректное имя.",
  invalid_json: "Некорректный запрос.",
  invalid_filename: "Некорректное имя файла.",
  invalid_mods_dir: "Некорректный каталог модов.",
  invalid_config: "Некорректная конфигурация.",
  invalid_limit: "Некорректный лимит.",
  invalid_dest: "Некорректный путь.",
  invalid_version: "Некорректная версия.",
  forbidden: "Недостаточно прав.",
  not_found: "Не найдено.",
  conflict: "Такая запись уже есть.",
  invite_invalid: "Приглашение недействительно.",
  username_taken: "Это имя уже занято.",
  mods_dir_unset: "Сначала укажите каталог модов на сервере.",
  slug_exists: "Такой идентификатор мода уже есть.",
  restart_in_progress: "Перезапуск уже выполняется.",
  rcon_failed: "Не удалось отправить команду в игру.",
  restart_failed: "Не удалось перезапустить сервер.",
  logs_failed: "Не удалось прочитать журнал.",
  backup_busy: "Файлы заняты, изменения не применены.",
  backup_failed: "Не удалось сделать бекап.",
  rollback_failed: "Не удалось развернуть бекап.",
  rate_limited: "Слишком много попыток.",
  payload_too_large: "Слишком большой запрос.",
  already_exists: "Такая версия уже есть.",
  not_legacy: "Дублирование версии доступно только для Legacy Launcher.",
  unsupported_os: "Сборка для этой ОС недоступна.",
  busy: "Обновление уже выполняется.",
};

function phraseFor(code: string, status: number): string {
  if (code === "internal") {
    return status > 0 ? `внутренняя ошибка сервера (HTTP ${status})` : "внутренняя ошибка сервера";
  }
  const mapped = phrases[code];
  if (mapped) return mapped;
  if (/^[a-z0-9_]+$/.test(code)) return "неизвестная ошибка";
  return code;
}

function withDetail(err: unknown, text: string): string {
  const raw = err instanceof ApiError ? err.detail : "";
  const detail = safeDetail(raw, "");
  if (!detail || detail === text) return text;
  return `${text.replace(/\.+$/, "")}: ${detail}`;
}

export function displayError(err: unknown): string {
  const code = errorCode(err);
  const status = err instanceof ApiError ? err.status : 0;
  return withDetail(err, phraseFor(code, status));
}

export function failureText(err: unknown): string {
  const shown = displayError(err);
  if (!shown.startsWith("внутренняя ошибка сервера")) return shown;
  const sentence = sentenceFrom(err);
  if (sentence) return sentence;
  const cut = shown.indexOf(": ");
  if (cut >= 0) {
    const tail = shown.slice(cut + 2).trim();
    if (/[А-Яа-яЁё]/.test(tail)) return tail;
  }
  return shown;
}

function sentenceFrom(err: unknown): string {
  if (!(err instanceof ApiError)) {
    return err instanceof Error ? cyrillicLine(err.message) : "";
  }
  const fromCode = cyrillicLine(err.code);
  if (fromCode) return fromCode;
  const fromDetail = cyrillicLine(err.detail);
  if (fromDetail) return fromDetail;
  const body = rawBody(err.log);
  const parsed = parseBody(body);
  if (typeof parsed === "string") {
    const line = cyrillicLine(parsed);
    if (line) return line;
  } else if (parsed && typeof parsed === "object") {
    const obj = parsed as { error?: unknown; message?: unknown; detail?: unknown };
    for (const value of [obj.error, obj.message, obj.detail]) {
      if (typeof value !== "string") continue;
      const line = cyrillicLine(value);
      if (line) return line;
    }
  }
  return cyrillicLine(body);
}

function cyrillicLine(text: string): string {
  for (const part of text.split(/\r?\n/)) {
    const line = part.trim();
    if (!line || line === "internal" || /^[a-z0-9_]+$/.test(line)) continue;
    if (!/[А-Яа-яЁё]/.test(line)) continue;
    if (/goroutine|panic|traceback|\.go:\d+/i.test(line)) continue;
    return line.length > 400 ? line.slice(0, 400) : line;
  }
  return "";
}

function rawBody(log: string): string {
  const parts = log.split("\n");
  if (parts.length >= 3 && /^HTTP \d+/.test(parts[1])) return parts.slice(2).join("\n");
  return log;
}

export function errorLog(err: unknown): string {
  if (err instanceof ApiError && err.log) return err.log;
  if (err instanceof Error && err.message) return err.message;
  return "";
}

export function displayReleaseError(err: unknown): string {
  if (errorCode(err) === "conflict") return withDetail(err, "эта версия уже выпущена");
  return displayError(err);
}

function normalizeMod(mod: Mod): Mod {
  return {
    ...mod,
    client_only: mod.client_only === true,
    client_required: mod.client_required === true,
    revoked: mod.revoked === true,
    configs: Array.isArray(mod.configs) ? mod.configs : [],
  };
}

function joinUrl(serverUrl: string, path: string): string {
  const base = serverUrl.endsWith("/") ? serverUrl.slice(0, -1) : serverUrl;
  return base + path;
}

async function request(
  serverUrl: string,
  path: string,
  init: { method?: string; token?: string; json?: unknown; body?: FormData },
): Promise<Response> {
  const headers = new Headers();
  if (init.token) headers.set("Authorization", `Bearer ${init.token}`);
  let body: BodyInit | undefined;
  if (init.body) {
    body = init.body;
  } else if (init.json !== undefined) {
    headers.set("Content-Type", "application/json");
    body = JSON.stringify(init.json);
  }
  return fetch(joinUrl(serverUrl, path), {
    method: init.method ?? (body ? "POST" : "GET"),
    headers,
    body,
    credentials: "omit",
  });
}

function apiErrorFrom(body: unknown, status: number, fallback: string, log: string): ApiError {
  const parts = errorParts(body);
  const code = parts.code === "internal" && fallback !== "internal" ? fallback : parts.code;
  return new ApiError(code, status, parts.detail, log);
}

async function readOk<T>(res: Response, where: string): Promise<T> {
  if (res.status === 401) {
    const text = await res.text().catch(() => "");
    const err = apiErrorFrom(parseBody(text), 401, "unauthorized", formatLog(where, 401, text));
    await unauthorized();
    throw err;
  }
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw apiErrorFrom(parseBody(text), res.status, "internal", formatLog(where, res.status, text));
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

function parseBody(text: string): unknown {
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function formatLog(where: string, status: number, text: string): string {
  return `${where}\nHTTP ${status}\n${text}`;
}

function call<T>(
  serverUrl: string,
  path: string,
  init: { method?: string; token?: string; json?: unknown; body?: FormData },
): Promise<T> {
  const method = init.method ?? (init.body || init.json !== undefined ? "POST" : "GET");
  return request(serverUrl, path, init).then((res) => readOk<T>(res, `${method} ${joinUrl(serverUrl, path)}`));
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

export async function login(serverUrl: string, username: string, password: string): Promise<LoginResult> {
  const body = await call<unknown>(serverUrl, "/api/auth/login", {
    method: "POST",
    json: { username, password },
  });
  return parseLogin(body);
}

function parseLogin(body: unknown): LoginResult {
  if (!body || typeof body !== "object") throw new ApiError("internal", 200);
  const raw = body as {
    token?: unknown;
    refresh_token?: unknown;
    expires_at?: unknown;
    admin?: unknown;
  };
  const admin = raw.admin;
  if (typeof raw.token !== "string" || raw.token === "" || !admin || typeof admin !== "object") {
    throw new ApiError("internal", 200);
  }
  const user = admin as Partial<AdminUser>;
  if (typeof user.id !== "string" || user.id === "" || typeof user.username !== "string" || user.username === "") {
    throw new ApiError("internal", 200);
  }
  const result: LoginResult = {
    token: raw.token,
    expires_at: typeof raw.expires_at === "string" ? raw.expires_at : "",
    admin: {
      id: user.id,
      username: user.username,
      root: user.root === true,
      created_at: typeof user.created_at === "string" ? user.created_at : "",
    },
  };
  if (typeof raw.refresh_token === "string" && raw.refresh_token !== "") result.refresh_token = raw.refresh_token;
  return result;
}

export function logout(serverUrl: string, token: string): Promise<void> {
  return call(serverUrl, "/api/auth/logout", { method: "POST", token });
}

export function me(serverUrl: string, token: string): Promise<{ admin: AdminUser; server_version: string }> {
  return call<{ admin: AdminUser; server_version?: string }>(serverUrl, "/api/auth/me", { token }).then((raw) => ({
    admin: raw.admin,
    server_version: raw.server_version ?? "",
  }));
}

export function listAdmins(serverUrl: string, token: string): Promise<{ admins: AdminUser[] }> {
  return call(serverUrl, "/api/admin/admins", { token });
}

export function deleteAdmin(serverUrl: string, token: string, id: string): Promise<void> {
  return call(serverUrl, `/api/admin/admins/${id}`, { method: "DELETE", token });
}

export function createInvite(
  serverUrl: string,
  token: string,
  ttlHours = 72,
): Promise<{ invite: Invite }> {
  return call(serverUrl, "/api/admin/invites", {
    method: "POST",
    token,
    json: { ttl_hours: ttlHours },
  });
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

function upload<T>(
  serverUrl: string,
  path: string,
  method: string,
  token: string,
  body: FormData,
  onProgress?: (loaded: number, total: number) => void,
): Promise<T> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open(method, joinUrl(serverUrl, path));
    xhr.setRequestHeader("Authorization", `Bearer ${token}`);
    xhr.upload.onprogress = (event) => {
      if (onProgress && event.lengthComputable) onProgress(event.loaded, event.total);
    };
    const where = `${method} ${joinUrl(serverUrl, path)}`;
    xhr.onload = () => {
      const res = new Response(xhr.responseText, { status: xhr.status, headers: { "Content-Type": xhr.getResponseHeader("Content-Type") ?? "" } });
      readOk<T>(res, where).then(resolve, reject);
    };
    xhr.onerror = () => reject(new ApiError("internal", 0, "", `${where}\nсеть`));
    xhr.send(body);
  });
}

export interface ServerBackupProgress {
  running: boolean;
  phase: string;
  files_done: number;
  files_total: number;
  bytes_done: number;
  bytes_total: number;
}

export type BackupScope = "world" | "full" | "mods";
export type BackupDest = "disk" | "s3";

export function asBackupScope(raw?: string): BackupScope {
  if (raw === "full" || raw === "mods") return raw;
  return "world";
}

export function asBackupDest(raw?: string): BackupDest {
  return raw === "s3" ? "s3" : "disk";
}

export function listServerBackups(
  serverUrl: string,
  token: string,
): Promise<{ backups: ServerBackup[]; daily_time: string; backup_scope: BackupScope; backup_bytes: number; free_bytes: number; s3: S3Settings }> {
  return call<{ backups?: ServerBackup[]; daily_time?: string; backup_scope?: string; backup_bytes?: number; free_bytes?: number; s3?: Partial<S3Settings> }>(serverUrl, "/api/admin/backups", { token }).then((raw) => ({
    backups: raw.backups ?? [],
    daily_time: raw.daily_time ?? "",
    backup_scope: asBackupScope(raw.backup_scope),
    backup_bytes: raw.backup_bytes ?? 0,
    free_bytes: raw.free_bytes ?? 0,
    s3: {
      endpoint: raw.s3?.endpoint ?? "",
      region: raw.s3?.region ?? "",
      bucket: raw.s3?.bucket ?? "",
      prefix: raw.s3?.prefix ?? "",
      access_key: raw.s3?.access_key ?? "",
      secret_set: !!raw.s3?.secret_set,
      dest: asBackupDest(raw.s3?.dest),
    },
  }));
}

export interface Datapack {
  name: string;
  description: string;
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

export function listDatapacks(serverUrl: string, token: string): Promise<{ datapacks: Datapack[] }> {
  return call<{ datapacks?: Datapack[] }>(serverUrl, "/api/admin/datapacks", { token }).then((raw) => ({
    datapacks: raw.datapacks ?? [],
  }));
}

export function uploadDatapack(serverUrl: string, token: string, file: File): Promise<{ datapack: Datapack }> {
  const body = new FormData();
  body.append("file", file, file.name);
  return upload(serverUrl, "/api/admin/datapacks", "POST", token, body);
}

export function listDatapackFiles(serverUrl: string, token: string, name: string): Promise<{ files: string[] }> {
  return call<{ files?: string[] }>(serverUrl, `/api/admin/datapacks/${encodeURIComponent(name)}/files`, { token }).then((raw) => ({
    files: raw.files ?? [],
  }));
}

export function readDatapackFile(serverUrl: string, token: string, name: string, path: string): Promise<{ path: string; text: string }> {
  const query = new URLSearchParams({ path });
  return call(serverUrl, `/api/admin/datapacks/${encodeURIComponent(name)}/file?${query}`, { token });
}

export function saveDatapackFile(serverUrl: string, token: string, name: string, path: string, text: string): Promise<void> {
  return call(serverUrl, `/api/admin/datapacks/${encodeURIComponent(name)}/file`, {
    method: "PUT",
    token,
    json: { path, text },
  });
}

export function saveDailyBackup(serverUrl: string, token: string, time: string): Promise<{ time: string }> {
  return call(serverUrl, "/api/admin/backups/time", { method: "PUT", token, json: { time } });
}

export function saveBackupScope(serverUrl: string, token: string, scope: BackupScope): Promise<{ scope: BackupScope }> {
  return call<{ scope?: string }>(serverUrl, "/api/admin/backups/scope", { method: "PUT", token, json: { scope } }).then((raw) => ({
    scope: asBackupScope(raw.scope),
  }));
}

export function saveBackupDest(serverUrl: string, token: string, dest: BackupDest): Promise<{ dest: BackupDest }> {
  return call<{ dest?: string }>(serverUrl, "/api/admin/backups/dest", { method: "PUT", token, json: { dest } }).then((raw) => ({
    dest: asBackupDest(raw.dest),
  }));
}

export function serverBackupProgress(
  serverUrl: string,
  token: string,
): Promise<ServerBackupProgress & { backups: ServerBackup[]; backup_bytes: number; free_bytes: number }> {
  return call<{ progress?: Partial<ServerBackupProgress>; backups?: ServerBackup[]; backup_bytes?: number; free_bytes?: number }>(serverUrl, "/api/admin/backups", { token }).then((raw) => ({
    running: !!raw.progress?.running,
    phase: raw.progress?.phase ?? "",
    files_done: raw.progress?.files_done ?? 0,
    files_total: raw.progress?.files_total ?? 0,
    bytes_done: raw.progress?.bytes_done ?? 0,
    bytes_total: raw.progress?.bytes_total ?? 0,
    backups: raw.backups ?? [],
    backup_bytes: raw.backup_bytes ?? 0,
    free_bytes: raw.free_bytes ?? 0,
  }));
}

export function createServerBackup(serverUrl: string, token: string): Promise<{ backup: ServerBackup }> {
  return call(serverUrl, "/api/admin/backups", { method: "POST", token });
}

export function createServerMilestone(serverUrl: string, token: string): Promise<{ backup: ServerBackup }> {
  return call(serverUrl, "/api/admin/backups/milestone", { method: "POST", token });
}

export interface S3Input {
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  access_key: string;
  secret_key: string;
}

export function saveServerS3(serverUrl: string, token: string, input: S3Input): Promise<{ s3: S3Settings }> {
  return call(serverUrl, "/api/admin/backups/s3", { method: "PUT", token, json: input });
}

export function checkServerS3(serverUrl: string, token: string, input: S3Input): Promise<{ message: string }> {
  return call(serverUrl, "/api/admin/backups/s3/check", { method: "POST", token, json: input });
}

export function launchServerBackup(serverUrl: string, token: string, id: string): Promise<BackupRollback & { restarted: boolean }> {
  return call<BackupRollback & { restarted?: boolean }>(serverUrl, `/api/admin/backups/${id}/launch`, { method: "POST", token }).then((raw) => ({
    backups: raw.backups ?? [],
    last_restart_at: raw.last_restart_at ?? "",
    pending_restart: raw.pending_restart ?? [],
    restarted: !!raw.restarted,
  }));
}

export function deleteServerBackup(serverUrl: string, token: string, id: string): Promise<{ backups: ServerBackup[] }> {
  return call<{ backups?: ServerBackup[] }>(serverUrl, `/api/admin/backups/${id}`, { method: "DELETE", token }).then((raw) => ({
    backups: raw.backups ?? [],
  }));
}

export function rollbackServerBackup(serverUrl: string, token: string, id: string): Promise<BackupRollback> {
  return call<BackupRollback>(serverUrl, `/api/admin/backups/${id}/rollback`, { method: "POST", token }).then((raw) => ({
    backups: raw.backups ?? [],
    last_restart_at: raw.last_restart_at ?? "",
    pending_restart: raw.pending_restart ?? [],
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
