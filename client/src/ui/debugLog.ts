import { failureText } from "../api";

export interface DebugLine {
  id: number;
  at: string;
  control: string;
  result: string;
}

let lines: DebugLine[] = [];
let seq = 0;
let redactions: string[] = [];
const listeners = new Set<() => void>();

export function setDebugRedactions(values: string[]): void {
  redactions = values.map((value) => value.trim()).filter((value) => value.length >= 8);
}

export function subscribeAdminDebug(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function adminDebugSnapshot(): DebugLine[] {
  return lines;
}

export function noteClick(control: string, result: string): void {
  push(control, result);
}

export function noteError(control: string, err: unknown): void {
  push(control, failureText(err));
}

function push(control: string, result: string): void {
  const at = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  lines = [
    ...lines,
    {
      id: ++seq,
      at: `${p(at.getHours())}:${p(at.getMinutes())}:${p(at.getSeconds())}`,
      control: scrub(control),
      result: scrub(result),
    },
  ];
  for (const listener of listeners) listener();
}

function scrub(text: string): string {
  let out = text.replace(/"(secret_key|password|secret|access_key)"\s*:\s*"(?:\\.|[^"\\])*"/gi, '"$1":"***"');
  for (const secret of redactions) {
    if (secret && out.includes(secret)) out = out.split(secret).join("***");
  }
  return out;
}
