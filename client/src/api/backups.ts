import { call } from "./http";
import type { BackupRollback, S3Settings, ServerBackup } from "../types";

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
