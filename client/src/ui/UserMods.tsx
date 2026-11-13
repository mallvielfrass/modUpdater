import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import { useState } from "react";
import { displayError, errorCode, unauthorized } from "../api";
import { applyModConfig, backupMod, ensureInstanceBackup, installMod } from "../commands";
import {
  backupFilenameFor,
  configsOutOfDate,
  isRequiredMod,
  updateSteps,
  type SyncCurrent,
  type SyncInstall,
  type SyncItem,
  type SyncReplace,
  type UpdateStep,
} from "../sync";
import type { Mod } from "../types";

type RemoteItem = SyncCurrent | SyncReplace | SyncInstall;

interface Props {
  items: SyncItem[] | null;
  registry: Record<string, string>;
  onChanged: () => Promise<void>;
  onBusy?: (busy: boolean) => void;
}

export function UserMods(props: Props) {
  const [busy, setBusy] = useState(false);
  const [indexText, setIndexText] = useState("");
  const [percent, setPercent] = useState(0);
  const [known, setKnown] = useState(false);
  const [error, setError] = useState("");

  function setLocked(locked: boolean) {
    setBusy(locked);
    props.onBusy?.(locked);
  }

  async function runSteps(steps: UpdateStep[]) {
    if (steps.length === 0 || busy) return;
    setLocked(true);
    setError("");
    setPercent(0);
    setKnown(false);
    let backupListen: UnlistenFn | null = null;
    try {
      backupListen = await listen<{ loaded: number; total: number }>("backup-progress", (event) => {
        const total = event.payload.total;
        setIndexText("бэкап");
        if (total > 0) {
          setKnown(true);
          setPercent(Math.round(Math.min(1, event.payload.loaded / total) * 100));
        } else {
          setKnown(false);
          setPercent(0);
        }
      });
      await ensureInstanceBackup();
    } catch (err) {
      setError(displayError(err));
      setIndexText("");
      setPercent(0);
      setLocked(false);
      return;
    } finally {
      backupListen?.();
    }
    setIndexText("");
    setPercent(0);
    setKnown(false);
    for (let index = 0; index < steps.length; index += 1) {
      const step = steps[index];
      setIndexText(`загрузка ${index + 1} / ${steps.length}`);
      let unlisten: UnlistenFn | null = null;
      try {
        unlisten = await listen<{ loaded: number; total: number }>("install-progress", (event) => {
          const total = event.payload.total;
          if (total <= 0) return;
          const part = Math.min(1, event.payload.loaded / total);
          setKnown(true);
          setPercent(Math.min(100, Math.round(((index + part) / steps.length) * 100)));
        });
      } catch {
        unlisten = null;
      }
      try {
        if (step.removeFilename) {
          setIndexText(`удаление ${index + 1} / ${steps.length}`);
          await backupMod(step.removeFilename);
          setPercent(Math.round(((index + 1) / steps.length) * 100));
          continue;
        }
        if (!step.mod) continue;
        await installMod(step.mod.id, step.mod.filename, step.mod.sha256, step.backupFilename);
        if (step.applyConfigs) {
          for (const cfg of step.mod.configs ?? []) {
            setIndexText(`конфиг ${cfg.dest}`);
            await applyModConfig(step.mod.id, cfg.id, cfg.dest, cfg.kind, cfg.sha256);
          }
        }
        setPercent(Math.round(((index + 1) / steps.length) * 100));
      } catch (err) {
        if (errorCode(err) === "unauthorized") await unauthorized();
        setError(`${step.mod?.name ?? step.removeFilename ?? ""}: ${displayError(err)}`);
        setIndexText("");
        setPercent(0);
        setLocked(false);
        try {
          await props.onChanged();
        } catch {
          // The install error stays visible.
        }
        return;
      } finally {
        unlisten?.();
      }
    }
    setIndexText("");
    setPercent(0);
    setLocked(false);
    try {
      await props.onChanged();
    } catch (err) {
      setError(displayError(err));
    }
  }

  async function removeLocal(filename: string) {
    if (busy) return;
    setLocked(true);
    setError("");
    try {
      await backupMod(filename);
      await props.onChanged();
    } catch (err) {
      setError(displayError(err));
    } finally {
      setLocked(false);
    }
  }

  async function applyConfigs(mod: Mod) {
    if (busy) return;
    const pending = (mod.configs ?? []).filter((cfg) => (props.registry[cfg.id] ?? "").toLowerCase() !== cfg.sha256.toLowerCase());
    if (pending.length === 0) return;
    setLocked(true);
    setError("");
    setPercent(0);
    setKnown(false);
    let unlisten: UnlistenFn | null = null;
    try {
      unlisten = await listen<{ loaded: number; total: number }>("config-progress", (event) => {
        const total = event.payload.total;
        if (total <= 0) return;
        setKnown(true);
        setPercent(Math.round(Math.min(1, event.payload.loaded / total) * 100));
      });
      for (const cfg of pending) {
        setIndexText(`конфиг ${cfg.dest}`);
        await applyModConfig(mod.id, cfg.id, cfg.dest, cfg.kind, cfg.sha256);
      }
      setIndexText("");
      setPercent(0);
      setLocked(false);
      await props.onChanged();
    } catch (err) {
      if (errorCode(err) === "unauthorized") await unauthorized();
      setError(displayError(err));
      setIndexText("");
      setPercent(0);
      setLocked(false);
      try {
        await props.onChanged();
      } catch {
        // The config error stays visible.
      }
    } finally {
      unlisten?.();
    }
  }

  function oneStep(item: RemoteItem): UpdateStep {
    if (item.action === "install") return { mod: item.mod, applyConfigs: true };
    const localFilename = item.action === "replace" ? item.localFilename : item.filename;
    const step: UpdateStep = { mod: item.mod };
    const backup = backupFilenameFor(item.mod.filename, localFilename);
    if (backup) step.backupFilename = backup;
    return step;
  }

  if (!props.items) {
    return <p className="hint">Нажмите «Проверить», чтобы сравнить список сервера с папкой модов.</p>;
  }

  const pending = updateSteps(props.items);
  const required = pending.filter((step) => (step.removeFilename ? step.clientOnly !== true || step.clientRequired === true : !!step.mod && isRequiredMod(step.mod)));
  const optional = pending.filter((step) => !!step.mod && step.mod.client_only === true && step.mod.client_required !== true);
  const installCount = props.items.filter((item) => item.action === "install").length;
  const replaceCount = props.items.filter((item) => item.action === "replace").length;
  const removeCount = props.items.filter((item) => item.action === "remove").length;
  const currentCount = props.items.filter((item) => item.action === "current").length;
  const rows = listRows(props.items);

  return (
    <section>
      <div className="updatebar">
        <div className="pills">
          <span className="pill pill-install">доустановить {installCount}</span>
          <span className="pill pill-replace">заменить {replaceCount}</span>
          <span className="pill pill-remove">удалить {removeCount}</span>
          <span className="pill pill-ok">установлен {currentCount}</span>
        </div>
        {required.length > 0 || optional.length > 0 ? (
          <div className="update-actions">
            {required.length > 0 ? (
              <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void runSteps(required)}>
                Доустановить обязательное
              </button>
            ) : null}
            {optional.length > 0 ? (
              <button type="button" className="btn" disabled={busy} onClick={() => void runSteps(optional)}>
                Установить необязательные
              </button>
            ) : null}
          </div>
        ) : null}
      </div>
      {indexText ? (
        <div className="meter">
          <div
            className="meter-track"
            role="progressbar"
            aria-valuemin={known ? 0 : undefined}
            aria-valuemax={known ? 100 : undefined}
            aria-valuenow={known ? percent : undefined}
            aria-valuetext={known ? `${indexText} · ${percent}%` : indexText}
          >
            <div className={known ? "meter-fill" : "meter-fill meter-indeterminate"} style={known ? { width: `${percent}%` } : undefined} />
          </div>
          <span className="progress">{known ? `${indexText} · ${percent}%` : indexText}</span>
        </div>
      ) : null}
      {error ? <p className="error">{error}</p> : null}
      <h2 className="section-title">Список модов</h2>
      {rows.length === 0 ? <p className="hint">Список пуст.</p> : null}
      <div className="mod-list">
        {rows.map((row) =>
          row.kind === "remote" ? (
            <RemoteRow
              key={row.item.mod.id}
              item={row.item}
              busy={busy}
              showConfig={row.item.action !== "install" && configsOutOfDate(row.item.mod, props.registry)}
              onUpdate={() => void runSteps([oneStep(row.item)])}
              onApply={() => void applyConfigs(row.item.mod)}
              onDelete={(filename) => void removeLocal(filename)}
            />
          ) : row.kind === "remove" ? (
            <article className="card mod-card" key={`remove:${row.filename}`}>
              <div className="mod-main">
                <div className="mod-title">
                  <StatusMark kind="remove" />
                  <span>{row.name}</span>
                </div>
              </div>
              <span className="pill pill-remove">удалить</span>
            </article>
          ) : (
            <article className="card mod-card" key={`extra:${row.filename}`}>
              <div className="mod-main">
                <div className="mod-title">
                  <StatusMark kind="extra" />
                  <span>{row.filename}</span>
                </div>
              </div>
              <button type="button" className="btn btn-quiet" disabled={busy} onClick={() => void removeLocal(row.filename)}>
                Удалить
              </button>
            </article>
          ),
        )}
      </div>
    </section>
  );
}

function listRows(items: SyncItem[]): ListRow[] {
  const rows: ListRow[] = [];
  for (const item of items) {
    if (item.action === "extra") rows.push({ kind: "extra", filename: item.local.filename });
    else if (item.action === "remove") rows.push({ kind: "remove", filename: item.filename, name: item.name });
    else rows.push({ kind: "remote", item });
  }
  rows.sort((left, right) => compareNameFile(rowSortName(left), rowSortFile(left), rowSortName(right), rowSortFile(right)));
  return rows;
}

function rowSortName(row: ListRow): string {
  if (row.kind === "extra") return row.filename;
  if (row.kind === "remove") return row.name;
  return row.item.mod.name;
}

function rowSortFile(row: ListRow): string {
  if (row.kind === "extra" || row.kind === "remove") return row.filename;
  return row.item.mod.filename;
}

type ListRow =
  | { kind: "remote"; item: RemoteItem }
  | { kind: "extra"; filename: string }
  | { kind: "remove"; filename: string; name: string };

function RemoteRow(props: {
  item: RemoteItem;
  busy: boolean;
  showConfig: boolean;
  onUpdate: () => void;
  onApply: () => void;
  onDelete: (filename: string) => void;
}) {
  const status =
    props.item.action === "current" ? "установлен" : props.item.action === "replace" ? "заменить" : "доустановить";
  const localName =
    props.item.action === "current"
      ? props.item.filename
      : props.item.action === "replace"
        ? props.item.localFilename
        : undefined;
  const pillClass =
    props.item.action === "current" ? "pill pill-ok" : props.item.action === "replace" ? "pill pill-replace" : "pill pill-install";
  const added = addedLabel(props.item.mod.created_at);
  return (
    <article className="card mod-card">
      <div className="mod-main">
        <div className="mod-title">
          <StatusMark kind={props.item.action} />
          <span>{props.item.mod.name}</span>
          {added ? <span className="ver">{added}</span> : null}
          {props.item.mod.client_only && props.item.mod.client_required !== true ? (
            <span className="badge badge-client">опциональный</span>
          ) : null}
          {isRequiredMod(props.item.mod) ? <span className="badge badge-client">обязательный</span> : null}
          <span className="ver">{props.item.mod.version}</span>
        </div>
        <p>{props.item.mod.description}</p>
      </div>
      <div className="mod-actions">
        <span className={pillClass}>{status}</span>
        {props.item.action === "current" ? null : (
          <button type="button" className="btn" disabled={props.busy} onClick={props.onUpdate}>
            {props.item.action === "install" ? "Доустановить" : "Обновить"}
          </button>
        )}
        {props.showConfig ? (
          <button type="button" className="btn" disabled={props.busy} onClick={props.onApply}>
            Накатить конфиг
          </button>
        ) : null}
        {localName ? (
          <button type="button" className="btn btn-quiet" disabled={props.busy} onClick={() => props.onDelete(localName)}>
            Удалить
          </button>
        ) : null}
      </div>
    </article>
  );
}

function StatusMark(props: { kind: RemoteItem["action"] | "extra" | "remove" }) {
  const label =
    props.kind === "current"
      ? "установлен"
      : props.kind === "replace"
        ? "заменить"
        : props.kind === "install"
          ? "доустановить"
          : props.kind === "remove"
            ? "удалить"
            : "только на этом компьютере";
  return (
    <svg className={`mark mark-${props.kind}`} viewBox="0 0 16 16" aria-label={label}>
      {props.kind === "current" ? <path d="M3 8.2 6.2 11.5 13 4.5" fill="none" stroke="currentColor" strokeWidth="2" /> : null}
      {props.kind === "replace" ? <path d="M8 2.2 14.2 13.5H1.8L8 2.2z" fill="currentColor" /> : null}
      {props.kind === "install" ? <path d="M8 2v8M4.5 7.5 8 11l3.5-3.5M3 13.5h10" fill="none" stroke="currentColor" strokeWidth="1.8" /> : null}
      {props.kind === "extra" ? <rect x="3.5" y="3.5" width="9" height="9" fill="currentColor" /> : null}
      {props.kind === "remove" ? <path d="M3.5 3.5 12.5 12.5M12.5 3.5 3.5 12.5" fill="none" stroke="currentColor" strokeWidth="1.8" /> : null}
    </svg>
  );
}

function compareNameFile(leftName: string, leftFile: string, rightName: string, rightFile: string): number {
  const byName = leftName.localeCompare(rightName, "ru", { sensitivity: "base" });
  if (byName !== 0) return byName;
  return leftFile.localeCompare(rightFile, "ru", { sensitivity: "base" });
}

export function addedLabel(iso: string | undefined): string {
  if (!iso) return "";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const day = String(date.getDate()).padStart(2, "0");
  const month = String(date.getMonth() + 1).padStart(2, "0");
  return `добавлен ${day}.${month}.${date.getFullYear()}`;
}
