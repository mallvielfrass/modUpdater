import { describe, expect, it } from "vitest";
import { ApiError, displayError, displayReleaseError, errorCodeFromBody, failureText, login } from "./api";

describe("error body", () => {
  it("reads the error code", () => {
    expect(errorCodeFromBody({ error: "unauthorized" })).toBe("unauthorized");
  });

  it("falls back when the body has no code", () => {
    expect(errorCodeFromBody({})).toBe("internal");
    expect(errorCodeFromBody(null)).toBe("internal");
    expect(errorCodeFromBody({ error: "" })).toBe("internal");
  });
});

describe("displayError", () => {
  it("turns internal into a sentence and keeps the status", () => {
    expect(displayError(new ApiError("internal", 500))).toBe("внутренняя ошибка сервера (HTTP 500)");
    expect(displayError(new ApiError("internal", 0))).toBe("внутренняя ошибка сервера");
  });

  it("appends a short detail and drops a stack", () => {
    expect(displayError(new ApiError("not_found", 404, "мод удалён"))).toBe("Не найдено: мод удалён");
    expect(displayError(new ApiError("internal", 500, "goroutine 1 panic"))).toBe("внутренняя ошибка сервера (HTTP 500)");
  });

  it("maps backup errors to Russian", () => {
    expect(displayError(new ApiError("backup_busy", 409))).toBe("Файлы заняты, изменения не применены.");
    expect(displayError(new ApiError("backup_failed", 502))).toBe("Не удалось сделать бекап.");
    expect(displayError(new ApiError("rollback_failed", 502))).toBe("Не удалось развернуть бекап.");
    expect(displayError(new ApiError("Некорректное время бекапа.", 400))).toBe("Некорректное время бекапа.");
    expect(displayError(new ApiError("Нет доступа к каталогу сервера.", 502))).toBe("Нет доступа к каталогу сервера.");
    expect(displayError(new ApiError("Не удалось скопировать файлы сервера.", 502))).toBe("Не удалось скопировать файлы сервера.");
  });

  it("names a released client version", () => {
    expect(displayReleaseError(new ApiError("conflict", 409))).toBe("эта версия уже выпущена");
  });

  it("keeps a Russian sentence from the body", () => {
    const log = "POST http://127.0.0.1/api/admin/backups\nHTTP 502\nНет доступа к каталогу сервера.";
    expect(failureText(new ApiError("internal", 502, "", log))).toBe("Нет доступа к каталогу сервера.");
    const json = 'POST http://127.0.0.1/api/admin/backups\nHTTP 502\n{"error":"internal","message":"Сначала создайте веху."}';
    expect(failureText(new ApiError("internal", 502, "", json))).toBe("Сначала создайте веху.");
    expect(failureText(new ApiError("Нет доступа к каталогу сервера.", 502))).toBe("Нет доступа к каталогу сервера.");
    expect(failureText(new ApiError("internal", 500))).toBe("внутренняя ошибка сервера (HTTP 500)");
  });
});

describe("login body", () => {
  it("accepts token, refresh_token and admin", async () => {
    const real = globalThis.fetch;
    globalThis.fetch = async () =>
      new Response(
        JSON.stringify({
          token: "a".repeat(64),
          refresh_token: "b".repeat(64),
          expires_at: "2026-01-01T00:00:00Z",
          admin: { id: "c".repeat(32), username: "cjv", root: true, created_at: "2026-01-01T00:00:00Z" },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    try {
      const result = await login("http://127.0.0.1:9", "cjv", "password1");
      expect(result.token).toHaveLength(64);
      expect(result.refresh_token).toHaveLength(64);
      expect(result.admin.username).toBe("cjv");
    } finally {
      globalThis.fetch = real;
    }
  });
});
