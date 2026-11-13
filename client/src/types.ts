export interface ModConfig {
  id: string;
  dest: string;
  kind: "file" | "dir";
  filename: string;
  sha256: string;
  size: number;
  updated_at: string;
}

export interface Mod {
  id: string;
  slug: string;
  name: string;
  description: string;
  filename: string;
  version: string;
  sha256: string;
  size: number;
  client: boolean;
  client_only: boolean;
  client_required?: boolean;
  revoked?: boolean;
  configs: ModConfig[];
  updated_at: string;
  created_at: string;
}

export interface PendingRestart {
  slug: string;
  name: string;
  filename: string;
  action: string;
  created_at: string;
}

export interface ServerBackup {
  id: string;
  created_at: string;
  current: boolean;
  label?: string;
  kind?: string;
  scope?: string;
  parent_id?: string;
  size?: number;
  sha256?: string;
  storage?: string;
  status?: string;
  error?: string;
}

export interface S3Settings {
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  access_key: string;
  secret_set: boolean;
  dest?: "disk" | "s3";
}

export interface BackupRollback {
  backups: ServerBackup[];
  last_restart_at: string;
  pending_restart: PendingRestart[];
}

export interface Event {
  id: string;
  mod_id: string;
  slug: string;
  name: string;
  action: "added" | "updated" | "deleted";
  filename: string;
  version: string;
  sha256: string;
  size: number;
  client: boolean;
  created_at: string;
  has_file: boolean;
}

export interface AdminUser {
  id: string;
  username: string;
  root: boolean;
  created_at: string;
}

export interface RevokedMod {
  id: string;
  slug: string;
  name: string;
  filename: string;
  sha256: string;
  client_only?: boolean;
  client_required?: boolean;
}

export interface LocalJar {
  filename: string;
  sha256: string;
  size: number;
  slug: string;
  version: string;
}

export interface Config {
  mods_dir: string;
}

export interface ClientLatest {
  version: string;
  linux_sha256: string;
  linux_size: number;
  windows_sha256: string;
  windows_size: number;
}

export interface ClientRelease {
  version: string;
  status: string;
  linux_sha256: string;
  linux_size: number;
  windows_sha256: string;
  windows_size: number;
  created_at: string;
}

export interface AdminReleases {
  active_version: string;
  releases: ClientRelease[];
}

export interface AdminModsResponse {
  mods: Mod[];
  last_restart_at: string;
  pending_restart: PendingRestart[];
}

export interface LauncherInfo {
  kind: "legacy" | "other";
  root: string;
  current_version: string;
  versions: string[];
}

export interface BackupOutcome {
  created: boolean;
  path: string;
}

export interface LocalBackup {
  id: string;
  version: string;
  created: number;
  bytes: number;
}

export interface ScanResult {
  mods_dir: string;
  added: number;
  updated: number;
  removed: number;
  conflicts: string[];
  skipped: string[];
}

export interface Invite {
  token: string;
  url: string;
  expires_at: string;
}

export interface LoginResult {
  token: string;
  refresh_token?: string;
  expires_at: string;
  admin: AdminUser;
}

export interface RestartResult {
  warned: boolean;
  restarted: boolean;
  last_restart_at: string;
  pending_restart: PendingRestart[];
  backup_error?: string;
}

export interface ServerLogs {
  unit: string;
  lines: string[];
}
