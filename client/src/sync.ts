import type { LocalJar, Mod, RevokedMod } from "./types";

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

export function isRequiredMod(mod: { client_only?: boolean; client_required?: boolean }): boolean {
  return mod.client_only !== true || mod.client_required === true;
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

  const findUnused = (pred: (jar: LocalJar) => boolean): number => {
    for (let index = 0; index < locals.length; index += 1) {
      if (!used[index] && pred(locals[index])) return index;
    }
    return -1;
  };

  for (const mod of remote) {
    const byHash = findUnused((jar) => jar.sha256 === mod.sha256);
    if (byHash >= 0) {
      used[byHash] = true;
      items.push({ action: "current", mod, filename: locals[byHash].filename });
      continue;
    }

    if (mod.slug !== "") {
      const slugMatches: number[] = [];
      for (let index = 0; index < locals.length; index += 1) {
        if (!used[index] && locals[index].slug === mod.slug) slugMatches.push(index);
      }
      if (slugMatches.length > 0) {
        const exact = slugMatches.find((index) => locals[index].filename === mod.filename);
        const chosen = exact !== undefined ? exact : slugMatches[0];
        used[chosen] = true;
        items.push({ action: "replace", mod, localFilename: locals[chosen].filename });
        continue;
      }
    }

    const byName = findUnused((jar) => jar.filename === mod.filename);
    if (byName >= 0) {
      used[byName] = true;
      items.push({ action: "replace", mod, localFilename: locals[byName].filename });
      continue;
    }

    items.push({ action: "install", mod });
  }

  for (const item of revoked) {
    const index = matchUnused(locals, used, item.sha256, item.slug, item.filename);
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

function matchUnused(locals: LocalJar[], used: boolean[], sha256: string, slug: string, filename: string): number {
  const findUnused = (pred: (jar: LocalJar) => boolean): number => {
    for (let index = 0; index < locals.length; index += 1) {
      if (!used[index] && pred(locals[index])) return index;
    }
    return -1;
  };
  const byHash = findUnused((jar) => jar.sha256 === sha256);
  if (byHash >= 0) return byHash;
  if (slug !== "") {
    const slugMatches: number[] = [];
    for (let index = 0; index < locals.length; index += 1) {
      if (!used[index] && locals[index].slug === slug) slugMatches.push(index);
    }
    if (slugMatches.length > 0) {
      const exact = slugMatches.find((index) => locals[index].filename === filename);
      return exact !== undefined ? exact : slugMatches[0];
    }
  }
  return findUnused((jar) => jar.filename === filename);
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
