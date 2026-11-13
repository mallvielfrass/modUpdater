import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import { useEffect, useState } from "react";
import {
  deleteRelease,
  displayReleaseError,
  errorLog,
  failureText,
  listReleases,
  releaseClientVersion,
} from "../../api";
import { launchDevRelease } from "../../commands";
import { pickAsset } from "../../semver";
import type { AdminReleases, ClientRelease } from "../../types";
import { noteClick } from "../debugLog";
import { ErrorNote } from "./ErrorNote";

export function ClientTab(props: { serverUrl: string; token: string; osName: string }) {
  const [data, setData] = useState<AdminReleases | null>(null);
  const [error, setError] = useState("");
  const [errorLogText, setErrorLogText] = useState("");
  const [busy, setBusy] = useState("");
  const [percent, setPercent] = useState(0);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const result = await listReleases(props.serverUrl, props.token);
        if (!cancelled) setData(result);
      } catch (err) {
        if (!cancelled) {
          setError(displayReleaseError(err));
          setErrorLogText(errorLog(err));
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [props.serverUrl, props.token, tick]);

  const devs = (data?.releases ?? []).filter((rel) => rel.status === "dev");

  async function launch(rel: ClientRelease) {
    const asset = pickAsset(props.osName, rel.linux_sha256, rel.linux_size, rel.windows_sha256, rel.windows_size);
    if (!asset || busy) return;
    setBusy(rel.version);
    setError("");
    setErrorLogText("");
    setPercent(0);
    let unlisten: UnlistenFn | null = null;
    try {
      unlisten = await listen<{ loaded: number; total: number }>("update-progress", (event) => {
        const total = event.payload.total;
        const pct = total > 0 ? Math.round((event.payload.loaded / total) * 100) : 0;
        setPercent(Math.min(100, pct));
      });
      await launchDevRelease(rel.version, asset.sha256, props.token);
      noteClick("Запустить", "ок");
    } catch (err) {
      setError(displayReleaseError(err));
      setErrorLogText(errorLog(err));
      noteClick("Запустить", clientFailure(err));
    } finally {
      unlisten?.();
      setBusy("");
      setPercent(0);
    }
  }

  async function publish(version: string) {
    if (busy) return;
    setBusy(version);
    setError("");
    setErrorLogText("");
    try {
      await releaseClientVersion(props.serverUrl, props.token, version);
      noteClick("Релиз", "ок");
      setTick((value) => value + 1);
    } catch (err) {
      setError(displayReleaseError(err));
      setErrorLogText(errorLog(err));
      noteClick("Релиз", clientFailure(err));
    } finally {
      setBusy("");
    }
  }

  async function reject(version: string) {
    if (busy) return;
    setBusy(version);
    setError("");
    setErrorLogText("");
    try {
      await deleteRelease(props.serverUrl, props.token, version);
      noteClick("Отклонить", "ок");
      setTick((value) => value + 1);
    } catch (err) {
      setError(displayReleaseError(err));
      setErrorLogText(errorLog(err));
      noteClick("Отклонить", clientFailure(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="stack">
      <p className="hint">{data?.active_version ? `У игроков: v${data.active_version}` : "релиза нет"}</p>
      <ErrorNote message={error} log={errorLogText} />
      {devs.length === 0 ? <p className="hint">dev-сборок нет</p> : null}
      {devs.map((rel) => {
        const asset = pickAsset(props.osName, rel.linux_sha256, rel.linux_size, rel.windows_sha256, rel.windows_size);
        const both = rel.linux_sha256 !== "" && rel.windows_sha256 !== "";
        return (
          <article className="card" key={rel.version}>
            <div className="mod-title">
              <span>v{rel.version}</span>
            </div>
            <p className="hint" title={rel.linux_sha256}>
              linux: {rel.linux_sha256 ? `есть ${rel.linux_sha256.slice(0, 12)}` : "нет"}
            </p>
            <p className="hint" title={rel.windows_sha256}>
              windows: {rel.windows_sha256 ? `есть ${rel.windows_sha256.slice(0, 12)}` : "нет"}
            </p>
            <div className="row">
              <button type="button" className="btn" disabled={!asset || busy !== ""} onClick={() => void launch(rel)}>
                Запустить
              </button>
              <button type="button" className="btn btn-primary" disabled={!both || busy !== ""} onClick={() => void publish(rel.version)}>
                Релиз
              </button>
              <button type="button" className="btn btn-quiet" disabled={busy !== ""} onClick={() => void reject(rel.version)}>
                Отклонить
              </button>
            </div>
            {busy === rel.version && percent > 0 ? <span className="progress">{percent}%</span> : null}
          </article>
        );
      })}
    </div>
  );
}

export function clientFailure(err: unknown): string {
  const shown = failureText(err);
  if (shown.startsWith("внутренняя ошибка сервера")) return shown;
  return displayReleaseError(err);
}
