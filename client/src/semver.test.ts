import { describe, expect, it } from "vitest";
import { isNewer, pickAsset } from "./semver";

describe("isNewer", () => {
  it("compares release versions", () => {
    expect(isNewer("0.1.1", "0.1.0")).toBe(true);
    expect(isNewer("0.1.0", "0.1.1")).toBe(false);
    expect(isNewer("0.1.0", "0.1.0")).toBe(false);
    expect(isNewer("1.0.0", "0.9.9")).toBe(true);
    expect(isNewer("", "0.1.0")).toBe(false);
    expect(isNewer("0.1.0", "0.1.0-rc.1")).toBe(true);
    expect(isNewer("0.1.0-rc.1", "0.1.0")).toBe(false);
    expect(isNewer("nope", "0.1.0")).toBe(false);
  });
});

describe("pickAsset", () => {
  it("returns the file for the current os only", () => {
    expect(pickAsset("linux", "abc", 10, "", 0)).toEqual({ sha256: "abc", size: 10 });
    expect(pickAsset("windows", "abc", 10, "def", 4)).toEqual({ sha256: "def", size: 4 });
    expect(pickAsset("linux", "", 0, "def", 4)).toBeNull();
    expect(pickAsset("other", "abc", 10, "def", 4)).toBeNull();
  });
});