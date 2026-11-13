declare const __MODUPDATER_SERVER_URL__: string;

export const SERVER_URL = __MODUPDATER_SERVER_URL__;

export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly detail: string;
  readonly log: string;

  constructor(code: string, status: number, detail = "", log = "") {
    super(code);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
    this.detail = detail;
    this.log = log;
  }
}

type UnauthorizedHandler = () => void | Promise<void>;

let onUnauthorized: UnauthorizedHandler | null = null;

export function setUnauthorizedHandler(handler: UnauthorizedHandler | null): void {
  onUnauthorized = handler;
}

export async function unauthorized(): Promise<void> {
  if (onUnauthorized) await onUnauthorized();
}

export function errorCodeFromBody(body: unknown): string {
  return errorParts(body).code;
}

function errorParts(body: unknown): { code: string; detail: string } {
  if (body && typeof body === "object" && "error" in body) {
    const obj = body as { error?: unknown; message?: unknown; detail?: unknown };
    const code = typeof obj.error === "string" && obj.error ? obj.error : "internal";
    const raw = typeof obj.detail === "string" ? obj.detail : typeof obj.message === "string" ? obj.message : "";
    return { code, detail: safeDetail(raw, code) };
  }
  return { code: "internal", detail: "" };
}

function safeDetail(raw: string, code: string): string {
  const text = raw.trim();
  if (!text || text === code || text.length > 180) return "";
  if (/[\r\n]/.test(text)) return "";
  if (/stack|goroutine|panic|traceback|\.go:\d+|\.tsx?:\d+|at\s+\S+\s+\(/i.test(text)) return "";
  return text;
}

export function errorCode(err: unknown): string {
  if (err instanceof ApiError) return err.code;
  if (typeof err === "string" && err) return err;
  if (err && typeof err === "object") {
    const message = (err as { message?: unknown }).message;
    if (typeof message === "string" && message) return message;
  }
  if (err instanceof Error && err.message) return err.message;
  return "internal";
}

const phrases: Record<string, string> = {
  unauthorized: "Сессия недействительна.",
  invalid_password: "Пароль не соответствует требованиям.",
  invalid_name: "Некорректное имя.",
  invalid_json: "Некорректный запрос.",
  invalid_filename: "Некорректное имя файла.",
  invalid_mods_dir: "Некорректный каталог модов.",
  invalid_config: "Некорректная конфигурация.",
  invalid_limit: "Некорректный лимит.",
  invalid_dest: "Некорректный путь.",
  invalid_version: "Некорректная версия.",
  forbidden: "Недостаточно прав.",
  not_found: "Не найдено.",
  conflict: "Такая запись уже есть.",
  invite_invalid: "Приглашение недействительно.",
  username_taken: "Это имя уже занято.",
  mods_dir_unset: "Сначала укажите каталог модов на сервере.",
  slug_exists: "Такой идентификатор мода уже есть.",
  restart_in_progress: "Перезапуск уже выполняется.",
  rcon_failed: "Не удалось отправить команду в игру.",
  restart_failed: "Не удалось перезапустить сервер.",
  logs_failed: "Не удалось прочитать журнал.",
  backup_busy: "Файлы заняты, изменения не применены.",
  backup_failed: "Не удалось сделать бекап.",
  rollback_failed: "Не удалось развернуть бекап.",
  rate_limited: "Слишком много попыток.",
  payload_too_large: "Слишком большой запрос.",
  already_exists: "Такая версия уже есть.",
  not_legacy: "Дублирование версии доступно только для Legacy Launcher.",
  unsupported_os: "Сборка для этой ОС недоступна.",
  busy: "Обновление уже выполняется.",
};

function phraseFor(code: string, status: number): string {
  if (code === "internal") {
    return status > 0 ? `внутренняя ошибка сервера (HTTP ${status})` : "внутренняя ошибка сервера";
  }
  const mapped = phrases[code];
  if (mapped) return mapped;
  if (/^[a-z0-9_]+$/.test(code)) return "неизвестная ошибка";
  return code;
}

function withDetail(err: unknown, text: string): string {
  const raw = err instanceof ApiError ? err.detail : "";
  const detail = safeDetail(raw, "");
  if (!detail || detail === text) return text;
  return `${text.replace(/\.+$/, "")}: ${detail}`;
}

export function displayError(err: unknown): string {
  const code = errorCode(err);
  const status = err instanceof ApiError ? err.status : 0;
  return withDetail(err, phraseFor(code, status));
}

export function failureText(err: unknown): string {
  const shown = displayError(err);
  if (!shown.startsWith("внутренняя ошибка сервера")) return shown;
  const sentence = sentenceFrom(err);
  if (sentence) return sentence;
  const cut = shown.indexOf(": ");
  if (cut >= 0) {
    const tail = shown.slice(cut + 2).trim();
    if (/[А-Яа-яЁё]/.test(tail)) return tail;
  }
  return shown;
}

function sentenceFrom(err: unknown): string {
  if (!(err instanceof ApiError)) {
    return err instanceof Error ? cyrillicLine(err.message) : "";
  }
  const fromCode = cyrillicLine(err.code);
  if (fromCode) return fromCode;
  const fromDetail = cyrillicLine(err.detail);
  if (fromDetail) return fromDetail;
  const body = rawBody(err.log);
  const parsed = parseBody(body);
  if (typeof parsed === "string") {
    const line = cyrillicLine(parsed);
    if (line) return line;
  } else if (parsed && typeof parsed === "object") {
    const obj = parsed as { error?: unknown; message?: unknown; detail?: unknown };
    for (const value of [obj.error, obj.message, obj.detail]) {
      if (typeof value !== "string") continue;
      const line = cyrillicLine(value);
      if (line) return line;
    }
  }
  return cyrillicLine(body);
}

function cyrillicLine(text: string): string {
  for (const part of text.split(/\r?\n/)) {
    const line = part.trim();
    if (!line || line === "internal" || /^[a-z0-9_]+$/.test(line)) continue;
    if (!/[А-Яа-яЁё]/.test(line)) continue;
    if (/goroutine|panic|traceback|\.go:\d+/i.test(line)) continue;
    return line.length > 400 ? line.slice(0, 400) : line;
  }
  return "";
}

function rawBody(log: string): string {
  const parts = log.split("\n");
  if (parts.length >= 3 && /^HTTP \d+/.test(parts[1])) return parts.slice(2).join("\n");
  return log;
}

export function errorLog(err: unknown): string {
  if (err instanceof ApiError && err.log) return err.log;
  if (err instanceof Error && err.message) return err.message;
  return "";
}

export function displayReleaseError(err: unknown): string {
  if (errorCode(err) === "conflict") return withDetail(err, "эта версия уже выпущена");
  return displayError(err);
}


function joinUrl(serverUrl: string, path: string): string {
  const base = serverUrl.endsWith("/") ? serverUrl.slice(0, -1) : serverUrl;
  return base + path;
}

async function request(
  serverUrl: string,
  path: string,
  init: { method?: string; token?: string; json?: unknown; body?: FormData },
): Promise<Response> {
  const headers = new Headers();
  if (init.token) headers.set("Authorization", `Bearer ${init.token}`);
  let body: BodyInit | undefined;
  if (init.body) {
    body = init.body;
  } else if (init.json !== undefined) {
    headers.set("Content-Type", "application/json");
    body = JSON.stringify(init.json);
  }
  return fetch(joinUrl(serverUrl, path), {
    method: init.method ?? (body ? "POST" : "GET"),
    headers,
    body,
    credentials: "omit",
  });
}

function apiErrorFrom(body: unknown, status: number, fallback: string, log: string): ApiError {
  const parts = errorParts(body);
  const code = parts.code === "internal" && fallback !== "internal" ? fallback : parts.code;
  return new ApiError(code, status, parts.detail, log);
}

async function readOk<T>(res: Response, where: string): Promise<T> {
  if (res.status === 401) {
    const text = await res.text().catch(() => "");
    const err = apiErrorFrom(parseBody(text), 401, "unauthorized", formatLog(where, 401, text));
    await unauthorized();
    throw err;
  }
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw apiErrorFrom(parseBody(text), res.status, "internal", formatLog(where, res.status, text));
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

function parseBody(text: string): unknown {
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function formatLog(where: string, status: number, text: string): string {
  return `${where}\nHTTP ${status}\n${text}`;
}

export function call<T>(
  serverUrl: string,
  path: string,
  init: { method?: string; token?: string; json?: unknown; body?: FormData },
): Promise<T> {
  const method = init.method ?? (init.body || init.json !== undefined ? "POST" : "GET");
  return request(serverUrl, path, init).then((res) => readOk<T>(res, `${method} ${joinUrl(serverUrl, path)}`));
}

export function upload<T>(
  serverUrl: string,
  path: string,
  method: string,
  token: string,
  body: FormData,
  onProgress?: (loaded: number, total: number) => void,
): Promise<T> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open(method, joinUrl(serverUrl, path));
    xhr.setRequestHeader("Authorization", `Bearer ${token}`);
    xhr.upload.onprogress = (event) => {
      if (onProgress && event.lengthComputable) onProgress(event.loaded, event.total);
    };
    const where = `${method} ${joinUrl(serverUrl, path)}`;
    xhr.onload = () => {
      const res = new Response(xhr.responseText, { status: xhr.status, headers: { "Content-Type": xhr.getResponseHeader("Content-Type") ?? "" } });
      readOk<T>(res, where).then(resolve, reject);
    };
    xhr.onerror = () => reject(new ApiError("internal", 0, "", `${where}\nсеть`));
    xhr.send(body);
  });
}
