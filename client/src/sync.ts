import { locate } from "./modlib";
import type { LocalJar, Mod, RevokedMod } from "./types";

export { isRequiredMod } from "./modlib";

export interface SyncCurrent {
  action: "current";
  mod: Mod;
  filename: string;
}

export interface SyncReplace {
  action: "replace";
  mod: Mod;
  localFilename: string;
}

export interface SyncInstall {
  action: "install";
  mod: Mod;
}

export interface SyncExtra {
  action: "extra";
  local: LocalJar;
}

export interface SyncRemove {
  action: "remove";
  filename: string;
  name: string;
  client_only?: boolean;
  client_required?: boolean;
}

export type SyncItem = SyncCurrent | SyncReplace | SyncInstall | SyncExtra | SyncRemove;

export interface UpdateStep {
  mod?: Mod;
  backupFilename?: string;
  applyConfigs?: boolean;
  removeFilename?: string;
  clientOnly?: boolean;
  clientRequired?: boolean;
}

function byFilename(left: LocalJar, right: LocalJar): number {
  if (left.filename < right.filename) return -1;
  if (left.filename > right.filename) return 1;
  return 0;
}

export function syncPlan(remote: Mod[], local: LocalJar[], revoked: RevokedMod[] = []): SyncItem[] {
  const locals = [...local].sort(byFilename);
  const used = new Array<boolean>(locals.length).fill(false);
  const items: SyncItem[] = [];

  for (const mod of remote) {
    const index = locate(locals, used, mod.sha256, mod.slug, mod.filename);
    if (index < 0) {
      items.push({ action: "install", mod });
      continue;
    }
    used[index] = true;
    if (locals[index].sha256 === mod.sha256) {
      items.push({ action: "current", mod, filename: locals[index].filename });
    } else {
      items.push({ action: "replace", mod, localFilename: locals[index].filename });
    }
  }

  for (const item of revoked) {
    const index = locate(locals, used, item.sha256, item.slug, item.filename);
    if (index < 0) continue;
    used[index] = true;
    items.push({
      action: "remove",
      filename: locals[index].filename,
      name: item.name || locals[index].filename,
      ...(item.client_only ? { client_only: true } : {}),
      ...(item.client_required ? { client_required: true } : {}),
    });
  }

  for (let index = 0; index < locals.length; index += 1) {
    if (!used[index]) items.push({ action: "extra", local: locals[index] });
  }
  return items;
}

export function configsOutOfDate(mod: Mod, registry: Record<string, string>): boolean {
  return (mod.configs ?? []).some((cfg) => (registry[cfg.id] ?? "").toLowerCase() !== cfg.sha256.toLowerCase());
}

export function backupFilenameFor(remoteFilename: string, localFilename: string | undefined): string | undefined {
  if (localFilename && localFilename !== remoteFilename) return localFilename;
  return undefined;
}

export function updateSteps(plan: SyncItem[]): UpdateStep[] {
  const remove: UpdateStep[] = [];
  const rest: UpdateStep[] = [];
  for (const item of plan) {
    if (item.action === "remove") {
      const step: UpdateStep = { removeFilename: item.filename };
      if (item.client_only) step.clientOnly = true;
      if (item.client_required) step.clientRequired = true;
      remove.push(step);
    } else if (item.action === "replace") {
      const step: UpdateStep = { mod: item.mod };
      const backup = backupFilenameFor(item.mod.filename, item.localFilename);
      if (backup) step.backupFilename = backup;
      rest.push(step);
    } else if (item.action === "install") {
      rest.push({ mod: item.mod, applyConfigs: true });
    }
  }
  return remove.concat(rest);
}
