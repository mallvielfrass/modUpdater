import { call, upload } from "./http";

export interface Datapack {
  name: string;
  description: string;
}

export function listDatapacks(serverUrl: string, token: string): Promise<{ datapacks: Datapack[] }> {
  return call<{ datapacks?: Datapack[] }>(serverUrl, "/api/admin/datapacks", { token }).then((raw) => ({
    datapacks: raw.datapacks ?? [],
  }));
}

export function uploadDatapack(serverUrl: string, token: string, file: File): Promise<{ datapack: Datapack }> {
  const body = new FormData();
  body.append("file", file, file.name);
  return upload(serverUrl, "/api/admin/datapacks", "POST", token, body);
}

export function listDatapackFiles(serverUrl: string, token: string, name: string): Promise<{ files: string[] }> {
  return call<{ files?: string[] }>(serverUrl, `/api/admin/datapacks/${encodeURIComponent(name)}/files`, { token }).then((raw) => ({
    files: raw.files ?? [],
  }));
}

export function readDatapackFile(serverUrl: string, token: string, name: string, path: string): Promise<{ path: string; text: string }> {
  const query = new URLSearchParams({ path });
  return call(serverUrl, `/api/admin/datapacks/${encodeURIComponent(name)}/file?${query}`, { token });
}

export function saveDatapackFile(serverUrl: string, token: string, name: string, path: string, text: string): Promise<void> {
  return call(serverUrl, `/api/admin/datapacks/${encodeURIComponent(name)}/file`, {
    method: "PUT",
    token,
    json: { path, text },
  });
}
