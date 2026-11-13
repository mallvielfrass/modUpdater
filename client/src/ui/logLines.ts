export type LogEvent = "all" | "error" | "session" | "chat";

const JOURNAL = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})\s+\S+\s+[^:]+:\s*/;
const CHAT = /^(?:\[[^\]\n]+\]\s+)?<[^>\s\n]{1,16}>\s+\S/;
const SESSION = / (?:joined|left) the game$/;
const LOGGED_IN = /\[\/[0-9A-Fa-f:.]+\] logged in\b/;

export function logMessage(line: string): string {
  const body = line.replace(JOURNAL, "");
  const mark = "]: ";
  const at = body.lastIndexOf(mark);
  return (at >= 0 ? body.slice(at + mark.length) : body).trim();
}

export function isErrorLine(line: string): boolean {
  return /\b(?:ERROR|FATAL)\b/.test(line) || /Exception\b/.test(line);
}

export function isChatLine(line: string): boolean {
  return CHAT.test(logMessage(line));
}

export function isSessionLine(line: string): boolean {
  const message = logMessage(line);
  if (CHAT.test(message)) return false;
  return SESSION.test(message) || LOGGED_IN.test(message);
}

export function filterLogLines(lines: readonly string[], event: LogEvent): string[] {
  if (event === "all") return lines.slice();
  return lines.filter((line) => {
    if (event === "error") return isErrorLine(line);
    if (event === "session") return isSessionLine(line);
    return isChatLine(line);
  });
}

export function logFileName(since: "hour" | "day" | "all", now: Date): string {
  const pad = (value: number) => String(value).padStart(2, "0");
  const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`;
  return `log-${since}-${stamp}.txt`;
}
