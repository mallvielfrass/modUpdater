import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import { useEffect, useState } from "react";
import { displayError } from "../api";
import { createInstanceBackup, duplicateVersion, inspectLauncher } from "../commands";
import type { LauncherInfo } from "../types";

interface Props {
  modsDir: string;
}

export function Backup(props: Props) {
  const [info, setInfo] = useState<LauncherInfo | null>(null);
  const [src, setSrc] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(0);
  const [total, setTotal] = useState(0);
  const [label, setLabel] = useState("");

  useEffect(() => {
    let cancelled = false;
    setError("");
    if (!props.modsDir.trim()) {
      setInfo(null);
      return;
    }
    void (async () => {
      try {
        const next = await inspectLauncher();
        if (cancelled) return;
        setInfo(next);
        setSrc(next.current_version);
        setName(next.current_version ? `${next.current_version} (copy 1)` : "");
      } catch (err) {
        if (!cancelled) setError(displayError(err));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [props.modsDir]);

  async function withProgress(text: string, work: () => Promise<void>) {
    if (busy) return;
    setBusy(true);
    setError("");
    setLabel(text);
    setDone(0);
    setTotal(0);
    let unlisten: UnlistenFn | null = null;
    try {
      unlisten = await listen<{ loaded: number; total: number }>("backup-progress", (event) => {
        setDone(event.payload.loaded);
        setTotal(event.payload.total);
      });
      await work();
    } catch (err) {
      setError(displayError(err));
    } finally {
      unlisten?.();
      setBusy(false);
      setLabel("");
      setDone(0);
      setTotal(0);
    }
  }

  const legacy = info?.kind === "legacy";
  const versions = !info
    ? []
    : info.current_version && !info.versions.includes(info.current_version)
      ? [info.current_version, ...info.versions]
      : info.versions;

  return (
    <section className="card">
      {info && !legacy ? <p className="hint">Дублирование версии доступно только для Legacy Launcher.</p> : null}
      {legacy && info ? (
        <>
          <div className="field">
            <label className="field-label" htmlFor="src-version">
              Исходная версия
            </label>
            <select id="src-version" value={src} onChange={(event) => setSrc(event.target.value)}>
              {versions.map((version) => (
                <option key={version} value={version}>
                  {version}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label className="field-label" htmlFor="new-version">
              Новая версия
            </label>
            <input id="new-version" value={name} onChange={(event) => setName(event.target.value)} />
          </div>
          <button
            type="button"
            className="btn btn-primary"
            disabled={busy || src.trim() === "" || name.trim() === ""}
            onClick={() => void withProgress("копирование", () => duplicateVersion(src, name.trim()))}
          >
            Дублировать версию
          </button>
        </>
      ) : null}
      <div className="form-actions">
        <button
          type="button"
          className="btn"
          disabled={busy || props.modsDir.trim() === ""}
          onClick={() => void withProgress("бэкап", async () => { await createInstanceBackup(); })}
        >
          Создать zip-бэкап текущей версии
        </button>
      </div>
      {label ? <ByteMeter label={label} done={done} total={total} /> : null}
      {error ? <p className="error">{error}</p> : null}
    </section>
  );
}

function ByteMeter(props: { label: string; done: number; total: number }) {
  const known = props.total > 0;
  const width = known ? Math.max(0, Math.min(100, (props.done / props.total) * 100)) : 0;
  const text = known ? `${props.label} · ${formatBytes(props.done)} / ${formatBytes(props.total)}` : props.label;
  return (
    <div className="meter">
      <div
        className="meter-track"
        role="progressbar"
        aria-valuemin={known ? 0 : undefined}
        aria-valuemax={known ? 100 : undefined}
        aria-valuenow={known ? Math.round(width) : undefined}
        aria-valuetext={text}
      >
        <div className={known ? "meter-fill" : "meter-fill meter-indeterminate"} style={known ? { width: `${width}%` } : undefined} />
      </div>
      <span className="progress">{text}</span>
    </div>
  );
}

function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "0 Б";
  if (n < 1024) return `${Math.round(n)} Б`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} КБ`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} МБ`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(1)} ГБ`;
}
