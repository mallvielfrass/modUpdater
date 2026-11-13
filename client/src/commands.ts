import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { open } from "@tauri-apps/plugin-dialog";
import type { BackupOutcome, Config, LauncherInfo, LocalBackup, LocalJar } from "./types";

export function loadConfig(): Promise<Config> {
  return invoke<Config>("load_config");
}

export function saveConfig(modsDir: string): Promise<void> {
  return invoke("save_config", { modsDir });
}

export function loadSession(): Promise<string> {
  return invoke<string>("load_session");
}

export function saveSession(token: string): Promise<void> {
  return invoke("save_session", { token });
}

export function scanMods(): Promise<LocalJar[]> {
  return invoke<LocalJar[]>("scan_mods");
}

export function installMod(
  modId: string,
  filename: string,
  sha256: string,
  backupFilename?: string,
): Promise<void> {
  return invoke("install_mod", {
    modId,
    filename,
    sha256,
    backupFilename: backupFilename ?? null,
  });
}

export function backupMod(filename: string): Promise<void> {
  return invoke("backup_mod", { filename });
}

export function appChannel(): Promise<string> {
  return invoke<string>("app_channel");
}

export function currentOs(): Promise<string> {
  return invoke<string>("current_os");
}

export function selfUpdate(version: string, sha256: string): Promise<void> {
  return invoke("self_update", { version, sha256 });
}

export function launchDevRelease(version: string, sha256: string, token: string): Promise<void> {
  return invoke("launch_dev_release", { version, sha256, token });
}

export function saveLogFile(filename: string, text: string): Promise<boolean> {
  return invoke<boolean>("save_log", { filename, text });
}

export async function downloadBackupZip(
  serverUrl: string,
  token: string,
  id: string,
  filename: string,
  onProgress: (loaded: number, total: number) => void,
): Promise<boolean> {
  const unlisten = await listen<{ loaded: number; total: number }>("backup-download", (event) => {
    onProgress(event.payload.loaded, event.payload.total);
  });
  try {
    return await invoke<boolean>("download_backup", { serverUrl, token, id, filename });
  } finally {
    unlisten();
  }
}

export async function uploadWorldFolder(
  serverUrl: string,
  token: string,
  onProgress: (loaded: number, total: number) => void,
): Promise<boolean> {
  const unlisten = await listen<{ loaded: number; total: number }>("world-upload", (event) => {
    onProgress(event.payload.loaded, event.payload.total);
  });
  try {
    return await invoke<boolean>("upload_world", { serverUrl, token });
  } finally {
    unlisten();
  }
}

export async function packDirZip(): Promise<File | null> {
  const packed = await invoke<{ filename: string; bytes: number[] } | null>("pack_dir_zip");
  if (!packed) return null;
  return new File([new Uint8Array(packed.bytes)], packed.filename, { type: "application/zip" });
}

export function applyModConfig(
  modId: string,
  configId: string,
  dest: string,
  kind: "file" | "dir",
  sha256: string,
): Promise<void> {
  return invoke("apply_mod_config", { modId, configId, dest, kind, sha256 });
}

export function loadConfigRegistry(): Promise<Record<string, string>> {
  return invoke<Record<string, string>>("load_config_registry");
}

export function ensureInstanceBackup(): Promise<BackupOutcome> {
  return invoke<BackupOutcome>("ensure_instance_backup");
}

export function createInstanceBackup(): Promise<BackupOutcome> {
  return invoke<BackupOutcome>("create_instance_backup");
}

export function listInstanceBackups(): Promise<LocalBackup[]> {
  return invoke<LocalBackup[]>("list_instance_backups");
}

export function openInstanceBackup(id: string): Promise<void> {
  return invoke("open_instance_backup", { id });
}

export function restoreInstanceBackup(id: string): Promise<void> {
  return invoke("restore_instance_backup", { id });
}

export function inspectLauncher(): Promise<LauncherInfo> {
  return invoke<LauncherInfo>("inspect_launcher");
}

export function duplicateVersion(src: string, newName: string): Promise<void> {
  return invoke("duplicate_version", { src, newName });
}

export async function pickModsDir(): Promise<string | null> {
  const selected = await open({ directory: true, multiple: false });
  if (typeof selected === "string" && selected.length > 0) return selected;
  return null;
}
