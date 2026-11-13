import type { LocalJar } from "./types";

export function isRequiredMod(mod: { client_only?: boolean; client_required?: boolean }): boolean {
  return mod.client_only !== true || mod.client_required === true;
}

export function locate(locals: LocalJar[], used: boolean[], sha256: string, slug: string, filename: string): number {
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
