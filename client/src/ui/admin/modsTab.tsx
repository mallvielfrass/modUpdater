import { useState } from "react";
import {
  createMod,
  deleteModConfig,
  importModrinth,
  revokeMod,
  updateMod,
  uploadModConfig,
  type ModrinthImport,
} from "../../api";
import { packDirZip } from "../../commands";
import type { Mod } from "../../types";
import { noteClick, noteError } from "../debugLog";
import { addedLabel } from "../UserMods";
import { FileButton } from "./FileButton";

export type ModSort = "alphabet" | "installed";
export type ModSide = "all" | "server" | "client" | "client_only";

export function AddMod(props: {
  serverUrl: string;
  token: string;
  onSaved: () => void;
  onError: (err: unknown) => void;
}) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [clientOnly, setClientOnly] = useState(false);
  const [clientRequired, setClientRequired] = useState(false);
  const [client, setClient] = useState(false);
  const [file, setFile] = useState<File | null>(null);
  const [progress, setProgress] = useState<number | null>(null);

  async function submit() {
    if (!file || progress !== null) return;
    setProgress(0);
    try {
      await createMod(
        props.serverUrl,
        props.token,
        file,
        {
          name: name.trim() || undefined,
          description: description.trim() || undefined,
          client: clientOnly || client,
          clientOnly,
          clientRequired: clientOnly ? clientRequired : false,
        },
        (loaded, total) => setProgress(Math.round((loaded / total) * 100)),
      );
      setName("");
      setDescription("");
      setClientOnly(false);
      setClientRequired(false);
      setClient(false);
      setFile(null);
      setProgress(null);
      noteClick("Добавить", "ок");
      props.onSaved();
    } catch (err) {
      setProgress(null);
      noteError("Добавить", err);
      props.onError(err);
    }
  }

  return (
    <div className="card compact-mod">
      <FileButton file={file} disabled={progress !== null} onFile={setFile} />
      <input aria-label="Название" placeholder="Название" value={name} onChange={(event) => setName(event.target.value)} />
      <input
        aria-label="Описание"
        placeholder="Описание"
        value={description}
        onChange={(event) => setDescription(event.target.value)}
      />
      <label className="check">
        <input
          type="checkbox"
          checked={clientOnly}
          onChange={(event) => {
            const next = event.target.checked;
            setClientOnly(next);
            if (next) setClient(true);
          }}
        />
        Только клиент
      </label>
      {clientOnly ? (
        <label className="check">
          <input type="checkbox" checked={clientRequired} onChange={(event) => setClientRequired(event.target.checked)} />
          Обязательный
        </label>
      ) : null}
      <label className="check">
        <input
          type="checkbox"
          checked={clientOnly || client}
          disabled={clientOnly}
          onChange={(event) => setClient(event.target.checked)}
        />
        Клиент
      </label>
      <button type="button" className="btn btn-primary" disabled={progress !== null} onClick={() => void submit()}>
        Добавить
      </button>
      {progress !== null ? <span className="progress">загрузка {progress}%</span> : null}
    </div>
  );
}

export function ImportModrinth(props: {
  serverUrl: string;
  token: string;
  onSaved: () => void;
  onError: (err: unknown) => void;
}) {
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<ModrinthImport | null>(null);

  async function submit() {
    if (!file || busy) return;
    setBusy(true);
    setResult(null);
    try {
      const imported = await importModrinth(props.serverUrl, props.token, file);
      setResult(imported);
      setFile(null);
      noteClick("Загрузить Modrinth", "ок");
      props.onSaved();
    } catch (err) {
      noteError("Загрузить Modrinth", err);
      props.onError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card compact-mod">
      <label className={`btn file-btn${busy ? " is-disabled" : ""}`}>
        {file ? file.name : "modrinth.index.json"}
        <input
          type="file"
          accept=".json,application/json"
          disabled={busy}
          onChange={(event) => {
            setFile(event.target.files?.[0] ?? null);
            event.currentTarget.value = "";
          }}
        />
      </label>
      <button type="button" className="btn btn-primary" disabled={busy || !file} onClick={() => void submit()}>
        Загрузить Modrinth
      </button>
      {busy ? <span className="progress">загрузка</span> : null}
      {result ? <ImportReport result={result} /> : null}
    </div>
  );
}

export function ImportReport(props: { result: ModrinthImport }) {
  const added = props.result.added_files ?? [];
  const skipped = props.result.skipped_files ?? [];
  const failed = props.result.failed ?? [];
  return (
    <details className="import-log">
      <summary>
        {`скачано ${props.result.added}, обновлено ${props.result.updated}, пропущено ${props.result.skipped}, ошибок ${failed.length}`}
      </summary>
      <p>добавлены</p>
      <ul>
        {added.length > 0 ? added.map((name) => <li key={`a-${name}`}>{name}</li>) : <li>нет</li>}
      </ul>
      <p>пропущены</p>
      <ul>
        {skipped.length > 0 ? skipped.map((name) => <li key={`s-${name}`}>{name}</li>) : <li>нет</li>}
      </ul>
      <p>ошибки</p>
      <ul>
        {failed.length > 0 ? failed.map((name) => <li key={`f-${name}`}>{name}</li>) : <li>нет</li>}
      </ul>
    </details>
  );
}

export function compareMods(a: Mod, b: Mod): number {
  const byName = a.name.localeCompare(b.name, "ru", { sensitivity: "base" });
  if (byName !== 0) return byName;
  return a.filename.localeCompare(b.filename, "ru", { sensitivity: "base" });
}

export function compareInstalled(a: Mod, b: Mod): number {
  const byDate = b.created_at.localeCompare(a.created_at);
  if (byDate !== 0) return byDate;
  return compareMods(a, b);
}

export function listActiveMods(mods: Mod[], query: string, side: ModSide, sort: ModSort): Mod[] {
  const needle = query.trim().toLowerCase();
  const matched = mods.filter((mod) => {
    if (mod.revoked) return false;
    if (needle !== "" && !mod.name.toLowerCase().includes(needle) && !mod.filename.toLowerCase().includes(needle)) return false;
    if (side === "server") return mod.client === false;
    if (side === "client") return mod.client === true && mod.client_only === false;
    if (side === "client_only") return mod.client_only === true;
    return true;
  });
  matched.sort(sort === "installed" ? compareInstalled : compareMods);
  return matched;
}

export function ModRow(props: {
  mod: Mod;
  serverUrl: string;
  token: string;
  onSaved: () => void;
  onError: (err: unknown) => void;
}) {
  const [name, setName] = useState(props.mod.name);
  const [description, setDescription] = useState(props.mod.description);
  const [client, setClient] = useState(props.mod.client);
  const [progress, setProgress] = useState<number | null>(null);
  const [dest, setDest] = useState("");
  const labelsDirty = name !== props.mod.name || description !== props.mod.description;
  const added = addedLabel(props.mod.created_at);

  async function saveLabels() {
    try {
      await updateMod(props.serverUrl, props.token, props.mod.id, { name, description });
      noteClick("Сохранить", "ок");
      props.onSaved();
    } catch (err) {
      noteError("Сохранить", err);
      props.onError(err);
    }
  }

  async function changeRequired(next: boolean) {
    try {
      await updateMod(props.serverUrl, props.token, props.mod.id, { clientRequired: next });
      props.onSaved();
    } catch (err) {
      props.onError(err);
    }
  }

  async function changeClient(next: boolean) {
    setClient(next);
    try {
      await updateMod(props.serverUrl, props.token, props.mod.id, { client: next });
      props.onSaved();
    } catch (err) {
      setClient(!next);
      props.onError(err);
    }
  }

  async function replaceFile(next: File | null) {
    if (!next || progress !== null) return;
    setProgress(0);
    try {
      await updateMod(props.serverUrl, props.token, props.mod.id, { file: next }, (loaded, total) => {
        setProgress(Math.round((loaded / total) * 100));
      });
      setProgress(null);
      props.onSaved();
    } catch (err) {
      setProgress(null);
      props.onError(err);
    }
  }

  async function revoke() {
    try {
      await revokeMod(props.serverUrl, props.token, props.mod.id);
      noteClick("Отозвать", "ок");
      props.onSaved();
    } catch (err) {
      noteError("Отозвать", err);
      props.onError(err);
    }
  }

  async function addFile(next: File | null) {
    const target = dest.trim();
    if (!next || !target || progress !== null) return;
    setProgress(0);
    try {
      await uploadModConfig(props.serverUrl, props.token, props.mod.id, target, "file", next, (loaded, total) => {
        setProgress(Math.round((loaded / total) * 100));
      });
      setDest("");
      setProgress(null);
      props.onSaved();
    } catch (err) {
      setProgress(null);
      props.onError(err);
    }
  }

  async function addDir() {
    const target = dest.trim();
    if (!target || progress !== null) return;
    setProgress(0);
    try {
      const packed = await packDirZip();
      if (!packed) {
        setProgress(null);
        noteClick("Папка", "отменено");
        return;
      }
      await uploadModConfig(props.serverUrl, props.token, props.mod.id, target, "dir", packed, (loaded, total) => {
        setProgress(Math.round((loaded / total) * 100));
      });
      setDest("");
      setProgress(null);
      noteClick("Папка", "ок");
      props.onSaved();
    } catch (err) {
      setProgress(null);
      noteError("Папка", err);
      props.onError(err);
    }
  }

  async function removeConfig(configId: string) {
    try {
      await deleteModConfig(props.serverUrl, props.token, props.mod.id, configId);
      noteClick("Удалить конфиг", "ок");
      props.onSaved();
    } catch (err) {
      noteError("Удалить конфиг", err);
      props.onError(err);
    }
  }

  return (
    <article className="card mod-admin">
      <div className="compact-mod">
        <div className="compact-name">
          <span className="filename">{props.mod.filename}</span>
          <span className="ver">{props.mod.version}</span>
          {added ? <span className="ver">{added}</span> : null}
          {props.mod.client_only ? <span className="badge badge-client">клиент</span> : null}
          {props.mod.revoked ? <span className="badge">отозван</span> : null}
        </div>
        <input aria-label="Название" placeholder="Название" value={name} onChange={(event) => setName(event.target.value)} />
        <input
          aria-label="Описание"
          placeholder="Описание"
          value={description}
          onChange={(event) => setDescription(event.target.value)}
        />
        <label className="check">
          <input
            type="checkbox"
            checked={props.mod.client_only || client}
            disabled={props.mod.client_only}
            onChange={(event) => void changeClient(event.target.checked)}
          />
          Клиент
        </label>
        {props.mod.client_only ? (
          <label className="check">
            <input
              type="checkbox"
              checked={props.mod.client_required === true}
              onChange={(event) => void changeRequired(event.target.checked)}
            />
            Обязательный
          </label>
        ) : null}
        <FileButton file={null} disabled={progress !== null} onFile={(next) => void replaceFile(next)} />
        {progress !== null ? <span className="progress">загрузка {progress}%</span> : null}
        {labelsDirty ? (
          <button type="button" className="btn btn-primary" onClick={() => void saveLabels()}>
            Сохранить
          </button>
        ) : null}
        <button type="button" className="btn btn-quiet" onClick={() => void revoke()}>
          Отозвать
        </button>
      </div>
      {props.mod.client ? (
        <div className="config-box">
          {(props.mod.configs ?? []).map((cfg) => (
            <div className="row" key={cfg.id}>
              <span className="filename">{cfg.dest}</span>
              <span className="hint">{cfg.kind === "dir" ? "папка" : "файл"}</span>
              <button type="button" className="btn btn-quiet" onClick={() => void removeConfig(cfg.id)}>
                Удалить
              </button>
            </div>
          ))}
          <div className="row">
            <input
              aria-label="Путь конфига"
              placeholder="путь конфига"
              value={dest}
              onChange={(event) => setDest(event.target.value)}
            />
            <label className={`btn file-btn${progress !== null || dest.trim() === "" ? " is-disabled" : ""}`}>
              Файл
              <input
                type="file"
                disabled={progress !== null || dest.trim() === ""}
                onChange={(event) => {
                  void addFile(event.target.files?.[0] ?? null);
                  event.currentTarget.value = "";
                }}
              />
            </label>
            <button type="button" className="btn" disabled={progress !== null || dest.trim() === ""} onClick={() => void addDir()}>
              Папка
            </button>
          </div>
        </div>
      ) : null}
    </article>
  );
}

export function ModsTab(props: {
  serverUrl: string;
  token: string;
  scanText: string;
  onScan: () => void;
  modQuery: string;
  setModQuery: (value: string) => void;
  modSort: ModSort;
  setModSort: (value: ModSort) => void;
  modSide: ModSide;
  setModSide: (value: ModSide) => void;
  activeMods: Mod[];
  clearError: () => void;
  reload: () => void;
  report: (err: unknown) => void;
}) {
  const { onScan, scanText, modQuery, setModQuery, modSort, setModSort, modSide, setModSide, activeMods, clearError, reload, report } = props;
  return (
        <div className="stack">
          <div className="scan-row">
            <button type="button" className="btn btn-primary" onClick={() => void onScan()}>
              Сканировать
            </button>
            {scanText ? <p className="hint">{scanText}</p> : null}
          </div>
          <AddMod
            serverUrl={props.serverUrl}
            token={props.token}
            onSaved={() => {
              clearError();
              reload();
            }}
            onError={report}
          />
          <ImportModrinth
            serverUrl={props.serverUrl}
            token={props.token}
            onSaved={() => {
              clearError();
              reload();
            }}
            onError={report}
          />
          <div className="mod-tools">
            <input
              aria-label="Поиск модов"
              placeholder="Поиск"
              value={modQuery}
              onChange={(event) => setModQuery(event.target.value)}
            />
            <select aria-label="Сортировка" value={modSort} onChange={(event) => setModSort(event.target.value as ModSort)}>
              <option value="alphabet">по алфавиту</option>
              <option value="installed">по дате установки</option>
            </select>
            <select aria-label="Фильтр" value={modSide} onChange={(event) => setModSide(event.target.value as ModSide)}>
              <option value="all">все</option>
              <option value="server">сервер</option>
              <option value="client">клиент</option>
              <option value="client_only">только клиент</option>
            </select>
          </div>
          {activeMods.map((mod) => (
            <ModRow
              key={`${mod.id}:${mod.updated_at}:${mod.sha256}`}
              mod={mod}
              serverUrl={props.serverUrl}
              token={props.token}
              onSaved={() => {
                clearError();
                reload();
              }}
              onError={report}
            />
          ))}
        </div>
  );
}
