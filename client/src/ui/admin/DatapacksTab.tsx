import { useEffect, useState } from "react";
import {
  listDatapackFiles,
  listDatapacks,
  readDatapackFile,
  saveDatapackFile,
  uploadDatapack,
  type Datapack,
} from "../../api";
import { noteClick, noteError } from "../debugLog";

export function DatapacksTab(props: {
  serverUrl: string;
  token: string;
  onSaved: () => void;
  onError: (err: unknown) => void;
}) {
  const [items, setItems] = useState<Datapack[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let cancelled = false;
    void listDatapacks(props.serverUrl, props.token)
      .then((result) => {
        if (!cancelled) setItems(result.datapacks);
      })
      .catch((err) => {
        if (!cancelled) props.onError(err);
      })
      .finally(() => {
        if (!cancelled) setLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, [props.serverUrl, props.token, tick]);

  async function submit() {
    if (!file || busy) return;
    setBusy(true);
    try {
      await uploadDatapack(props.serverUrl, props.token, file);
      setFile(null);
      noteClick("Загрузить датапак", "ок");
      props.onSaved();
      setTick((value) => value + 1);
    } catch (err) {
      noteError("Загрузить датапак", err);
      props.onError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="stack">
      <div className="card compact-mod">
        <label className={`btn file-btn${busy ? " is-disabled" : ""}`}>
          {file ? file.name : "Архив датапака"}
          <input
            type="file"
            accept=".zip,application/zip"
            disabled={busy}
            onChange={(event) => {
              setFile(event.target.files?.[0] ?? null);
              event.currentTarget.value = "";
            }}
          />
        </label>
        <button type="button" className="btn btn-primary" disabled={busy || !file} onClick={() => void submit()}>
          Загрузить
        </button>
      </div>
      {loaded && items.length === 0 ? <p className="hint">Датапаков нет.</p> : null}
      {items.map((item) => (
        <DatapackCard
          key={item.name}
          item={item}
          serverUrl={props.serverUrl}
          token={props.token}
          onSaved={props.onSaved}
          onError={props.onError}
        />
      ))}
    </div>
  );
}

export function DatapackCard(props: {
  item: Datapack;
  serverUrl: string;
  token: string;
  onSaved: () => void;
  onError: (err: unknown) => void;
}) {
  const [open, setOpen] = useState(false);
  const [files, setFiles] = useState<string[]>([]);
  const [path, setPath] = useState("");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);

  async function toggle() {
    if (open) {
      setOpen(false);
      noteClick("Конфиги", "закрыто");
      return;
    }
    setBusy(true);
    try {
      const result = await listDatapackFiles(props.serverUrl, props.token, props.item.name);
      setFiles(result.files);
      setPath("");
      setText("");
      setOpen(true);
      noteClick("Конфиги", "ок");
    } catch (err) {
      noteError("Конфиги", err);
      props.onError(err);
    } finally {
      setBusy(false);
    }
  }

  async function load(file: string) {
    setPath(file);
    if (file === "") {
      setText("");
      return;
    }
    setBusy(true);
    try {
      const result = await readDatapackFile(props.serverUrl, props.token, props.item.name, file);
      setText(result.text);
    } catch (err) {
      setText("");
      props.onError(err);
    } finally {
      setBusy(false);
    }
  }

  async function save() {
    if (path === "" || busy) return;
    setBusy(true);
    try {
      await saveDatapackFile(props.serverUrl, props.token, props.item.name, path, text);
      noteClick("Сохранить конфиг", "ок");
      props.onSaved();
    } catch (err) {
      noteError("Сохранить конфиг", err);
      props.onError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <article className="card datapack-card">
      <div className="datapack-head">
        <span>{props.item.name}</span>
        <button type="button" className="btn" disabled={busy} onClick={() => void toggle()}>
          Конфиги
        </button>
        {props.item.description ? <span className="hint">{props.item.description}</span> : null}
      </div>
      {open ? (
        <div className="stack datapack-files">
          {files.length === 0 ? <p className="hint">Текстовых файлов нет.</p> : null}
          {files.length > 0 ? (
            <label className="field">
              <span className="field-label">Файл</span>
              <select aria-label="Файл датапака" value={path} disabled={busy} onChange={(event) => void load(event.target.value)}>
                <option value="">выберите файл</option>
                {files.map((file) => (
                  <option key={file} value={file}>
                    {file}
                  </option>
                ))}
              </select>
            </label>
          ) : null}
          {path !== "" ? (
            <label className="field">
              <span className="field-label">{path}</span>
              <textarea value={text} spellCheck={false} disabled={busy} onChange={(event) => setText(event.target.value)} />
              <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void save()}>
                Сохранить
              </button>
            </label>
          ) : null}
        </div>
      ) : null}
    </article>
  );
}
