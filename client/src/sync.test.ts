import { describe, expect, it } from "vitest";
import { configsOutOfDate, isRequiredMod, syncPlan, updateSteps, type SyncItem } from "./sync";
import type { LocalJar, Mod } from "./types";

function jar(filename: string, sha256: string, slug: string): LocalJar {
  return { filename, sha256, size: 1, slug, version: "" };
}

function mod(fields: Partial<Mod> & Pick<Mod, "filename" | "sha256" | "slug">): Mod {
  return {
    id: fields.id ?? "a".repeat(32),
    slug: fields.slug,
    name: fields.name ?? fields.slug,
    description: fields.description ?? "описание",
    filename: fields.filename,
    version: fields.version ?? "1",
    sha256: fields.sha256,
    size: fields.size ?? 1,
    client: fields.client ?? true,
    client_only: fields.client_only ?? false,
    client_required: fields.client_required ?? false,
    configs: fields.configs ?? [],
    updated_at: fields.updated_at ?? "2020-01-01T00:00:00Z",
    created_at: fields.created_at ?? "",
  };
}

describe("syncPlan", () => {
  it("prefers a hash match over a different filename", () => {
    const local = [jar("b.jar", "hash", "b"), jar("a.jar", "hash", "a"), jar("slug.jar", "other", "remote")];
    const remote = [mod({ filename: "remote.jar", sha256: "hash", slug: "remote", name: "Remote" })];
    const plan = syncPlan(remote, local);
    expect(plan[0]).toEqual({ action: "current", mod: remote[0], filename: "a.jar" });
    expect(plan.filter((item) => item.action === "extra")).toHaveLength(2);
  });

  it("prefers the equal filename when several locals share a slug", () => {
    const local = [jar("a.jar", "1", "foo"), jar("b.jar", "2", "foo")];
    const remote = [mod({ filename: "b.jar", sha256: "9", slug: "foo" })];
    const plan = syncPlan(remote, local);
    expect(plan[0]).toMatchObject({ action: "replace", localFilename: "b.jar" });
    expect(plan[1]).toMatchObject({ action: "extra", local: local[0] });
  });

  it("picks the lexicographic filename when two slug matches differ", () => {
    const local = [jar("b.jar", "1", "foo"), jar("a.jar", "2", "foo")];
    const remote = [mod({ filename: "z.jar", sha256: "9", slug: "foo" })];
    const plan = syncPlan(remote, local);
    expect(plan[0]).toMatchObject({ action: "replace", localFilename: "a.jar" });
    expect(plan[1]).toMatchObject({ action: "extra", local: local[0] });
  });

  it("matches a filename when the slug differs", () => {
    const local = [jar("same.jar", "1", "local-slug")];
    const remote = [mod({ filename: "same.jar", sha256: "9", slug: "remote-slug" })];
    const plan = syncPlan(remote, local);
    expect(plan).toEqual([{ action: "replace", mod: remote[0], localFilename: "same.jar" }]);
  });

  it("marks an unused local jar as extra", () => {
    const extra = jar("extra.jar", "2", "extra");
    const keep = jar("keep.jar", "1", "keep");
    const remote = [mod({ filename: "keep.jar", sha256: "1", slug: "keep" })];
    const plan = syncPlan(remote, [keep, extra]);
    expect(plan.map((item) => item.action)).toEqual(["current", "extra"]);
    expect(plan[1]).toEqual({ action: "extra", local: extra });
  });

  it("uses each local jar once", () => {
    const local = [jar("a.jar", "h", "s")];
    const remote = [
      mod({ id: "1".repeat(32), filename: "remote-a.jar", sha256: "h", slug: "s", name: "A" }),
      mod({ id: "2".repeat(32), filename: "remote-b.jar", sha256: "h", slug: "s", name: "B" }),
    ];
    const plan = syncPlan(remote, local);
    expect(plan[0]).toMatchObject({ action: "current", filename: "a.jar" });
    expect(plan[1]).toEqual({ action: "install", mod: remote[1] });
    expect(plan.filter((item) => item.action === "extra")).toHaveLength(0);
  });

  it("does not match empty slugs", () => {
    const local = [jar("a.jar", "1", "")];
    const remote = [mod({ filename: "b.jar", sha256: "2", slug: "" })];
    expect(syncPlan(remote, local).map((item) => item.action)).toEqual(["install", "extra"]);
  });
});

describe("revoked mods", () => {
  it("removes a local jar that matches a revoked mod and leaves unrelated jars", () => {
    const local = [jar("old.jar", "h", "gone"), jar("mine.jar", "m", "mine")];
    const revoked = [{ id: "b".repeat(32), slug: "gone", name: "Gone", filename: "old.jar", sha256: "h" }];
    const plan = syncPlan([], local, revoked);
    expect(plan).toEqual([
      { action: "remove", filename: "old.jar", name: "Gone" },
      { action: "extra", local: local[1] },
    ]);
    expect(updateSteps(plan)).toEqual([{ removeFilename: "old.jar" }]);
  });

  it("does not remove a jar already claimed by an active mod", () => {
    const local = [jar("keep.jar", "h", "keep")];
    const remote = [mod({ filename: "keep.jar", sha256: "h", slug: "keep" })];
    const revoked = [{ id: "c".repeat(32), slug: "keep", name: "Keep", filename: "keep.jar", sha256: "h" }];
    expect(syncPlan(remote, local, revoked).map((item) => item.action)).toEqual(["current"]);
  });
});

describe("isRequiredMod", () => {
  it("treats a required client-only mod as required", () => {
    expect(isRequiredMod(mod({ filename: "a.jar", sha256: "1", slug: "a", client_only: true, client_required: true }))).toBe(true);
    expect(isRequiredMod(mod({ filename: "a.jar", sha256: "1", slug: "a", client_only: true }))).toBe(false);
    expect(isRequiredMod(mod({ filename: "a.jar", sha256: "1", slug: "a" }))).toBe(true);
  });
});

describe("updateSteps", () => {
  it("passes backup_filename only when the local filename differs", () => {
    const current = mod({ filename: "cur.jar", sha256: "4", slug: "v", name: "Current" });
    const replaced = mod({ filename: "new.jar", sha256: "1", slug: "s", name: "Replaced" });
    const same = mod({ filename: "same.jar", sha256: "2", slug: "t", name: "Same" });
    const added = mod({ filename: "add.jar", sha256: "3", slug: "u", name: "Added" });
    const plan: SyncItem[] = [
      { action: "current", mod: current, filename: "other.jar" },
      { action: "replace", mod: replaced, localFilename: "old.jar" },
      { action: "install", mod: added },
      { action: "replace", mod: same, localFilename: "same.jar" },
      { action: "extra", local: jar("x.jar", "9", "x") },
    ];
    expect(updateSteps(plan)).toEqual([
      { mod: replaced, backupFilename: "old.jar" },
      { mod: added, applyConfigs: true },
      { mod: same },
    ]);
    expect(updateSteps(plan)[1].backupFilename).toBeUndefined();
    expect(updateSteps(plan)[2].backupFilename).toBeUndefined();
  });
});

describe("configsOutOfDate", () => {
  it("is true when a config hash is missing or different", () => {
    const item = mod({
      filename: "a.jar",
      sha256: "1",
      slug: "a",
      configs: [
        { id: "ab".repeat(16), dest: "config/a.toml", kind: "file", filename: "a.toml", sha256: "abc", size: 1, updated_at: "" },
      ],
    });
    expect(configsOutOfDate(item, {})).toBe(true);
    expect(configsOutOfDate(item, { ["ab".repeat(16)]: "other" })).toBe(true);
    expect(configsOutOfDate(item, { ["ab".repeat(16)]: "abc" })).toBe(false);
  });
});
