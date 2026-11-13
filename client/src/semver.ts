export function parseSemver(raw: string): { major: number; minor: number; patch: number; pre: string[] } | null {
  if (!raw || raw.length > 64) return null;
  const dash = raw.indexOf("-");
  const core = dash === -1 ? raw : raw.slice(0, dash);
  const preRaw = dash === -1 ? "" : raw.slice(dash + 1);
  const parts = core.split(".");
  if (parts.length !== 3) return null;
  const nums: number[] = [];
  for (const part of parts) {
    if (!/^\d{1,6}$/.test(part) || (part.length > 1 && part.startsWith("0"))) return null;
    nums.push(Number(part));
  }
  if (dash === -1) return { major: nums[0], minor: nums[1], patch: nums[2], pre: [] };
  if (!preRaw || preRaw.length > 32) return null;
  const pre = preRaw.split(".");
  if (pre.some((part) => part.length === 0 || !/^[0-9A-Za-z-]+$/.test(part))) return null;
  return { major: nums[0], minor: nums[1], patch: nums[2], pre };
}

function cmpIdent(left: string, right: string): number {
  const leftNum = /^[0-9]+$/.test(left);
  const rightNum = /^[0-9]+$/.test(right);
  if (leftNum && rightNum) return Number(left) - Number(right);
  if (leftNum) return -1;
  if (rightNum) return 1;
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}

export function isNewer(latest: string, current: string): boolean {
  if (!latest) return false;
  const next = parseSemver(latest);
  const prev = parseSemver(current);
  if (!next || !prev) return false;
  if (next.major !== prev.major) return next.major > prev.major;
  if (next.minor !== prev.minor) return next.minor > prev.minor;
  if (next.patch !== prev.patch) return next.patch > prev.patch;
  if (next.pre.length === 0 && prev.pre.length === 0) return false;
  if (next.pre.length === 0) return true;
  if (prev.pre.length === 0) return false;
  const count = Math.max(next.pre.length, prev.pre.length);
  for (let index = 0; index < count; index += 1) {
    if (index >= next.pre.length) return false;
    if (index >= prev.pre.length) return true;
    const diff = cmpIdent(next.pre[index], prev.pre[index]);
    if (diff !== 0) return diff > 0;
  }
  return false;
}

export function pickAsset(
  os: string,
  linuxSha: string,
  linuxSize: number,
  windowsSha: string,
  windowsSize: number,
): { sha256: string; size: number } | null {
  const sha256 = os === "linux" ? linuxSha : os === "windows" ? windowsSha : "";
  const size = os === "linux" ? linuxSize : os === "windows" ? windowsSize : 0;
  if (!sha256 || !Number.isFinite(size) || size <= 0) return null;
  return { sha256, size };
}
