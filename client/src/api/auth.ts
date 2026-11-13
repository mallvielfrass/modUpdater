import { ApiError, call } from "./http";
import type { AdminUser, Invite, LoginResult } from "../types";

export async function login(serverUrl: string, username: string, password: string): Promise<LoginResult> {
  const body = await call<unknown>(serverUrl, "/api/auth/login", {
    method: "POST",
    json: { username, password },
  });
  return parseLogin(body);
}

function parseLogin(body: unknown): LoginResult {
  if (!body || typeof body !== "object") throw new ApiError("internal", 200);
  const raw = body as {
    token?: unknown;
    refresh_token?: unknown;
    expires_at?: unknown;
    admin?: unknown;
  };
  const admin = raw.admin;
  if (typeof raw.token !== "string" || raw.token === "" || !admin || typeof admin !== "object") {
    throw new ApiError("internal", 200);
  }
  const user = admin as Partial<AdminUser>;
  if (typeof user.id !== "string" || user.id === "" || typeof user.username !== "string" || user.username === "") {
    throw new ApiError("internal", 200);
  }
  const result: LoginResult = {
    token: raw.token,
    expires_at: typeof raw.expires_at === "string" ? raw.expires_at : "",
    admin: {
      id: user.id,
      username: user.username,
      root: user.root === true,
      created_at: typeof user.created_at === "string" ? user.created_at : "",
    },
  };
  if (typeof raw.refresh_token === "string" && raw.refresh_token !== "") result.refresh_token = raw.refresh_token;
  return result;
}

export function logout(serverUrl: string, token: string): Promise<void> {
  return call(serverUrl, "/api/auth/logout", { method: "POST", token });
}

export function me(serverUrl: string, token: string): Promise<{ admin: AdminUser; server_version: string }> {
  return call<{ admin: AdminUser; server_version?: string }>(serverUrl, "/api/auth/me", { token }).then((raw) => ({
    admin: raw.admin,
    server_version: raw.server_version ?? "",
  }));
}

export function listAdmins(serverUrl: string, token: string): Promise<{ admins: AdminUser[] }> {
  return call(serverUrl, "/api/admin/admins", { token });
}

export function deleteAdmin(serverUrl: string, token: string, id: string): Promise<void> {
  return call(serverUrl, `/api/admin/admins/${id}`, { method: "DELETE", token });
}

export function createInvite(
  serverUrl: string,
  token: string,
  ttlHours = 72,
): Promise<{ invite: Invite }> {
  return call(serverUrl, "/api/admin/invites", {
    method: "POST",
    token,
    json: { ttl_hours: ttlHours },
  });
}
