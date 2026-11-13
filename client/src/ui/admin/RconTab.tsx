import { useEffect, useRef, useState } from "react";
import { fetchRcon, type RconStatus } from "../../api";
import { noteClick, noteError } from "../debugLog";

export function RconTab(props: { serverUrl: string; token: string; onError: (err: unknown) => void }) {
  const [part, setPart] = useState<"players" | "mods">("players");
  const [data, setData] = useState<RconStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [tick, setTick] = useState(0);
  const asked = useRef(false);

  useEffect(() => {
    let cancelled = false;
    setBusy(true);
    void fetchRcon(props.serverUrl, props.token)
      .then((result) => {
        if (!cancelled) setData(result);
        if (!cancelled && asked.current) noteClick("Обновить", "ок");
      })
      .catch((err) => {
        if (!cancelled && asked.current) noteError("Обновить", err);
        if (!cancelled) props.onError(err);
      })
      .finally(() => {
        if (!cancelled) {
          asked.current = false;
          setBusy(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [props.serverUrl, props.token, tick]);

  const status = !data ? "сервер: …" : data.up ? (data.players_ok ? `сервер запущен, игроков ${data.online} из ${data.max}` : "сервер запущен") : "сервер не отвечает";

  return (
    <div className="stack">
      <div className="rcon-head">
        <p className="hint">{busy && data ? `${status} …` : status}</p>
        <button
          type="button"
          className="btn"
          disabled={busy}
          onClick={() => {
            asked.current = true;
            setTick((value) => value + 1);
          }}
        >
          Обновить
        </button>
      </div>
      <div className="tabs" role="tablist">
        <button
          type="button"
          role="tab"
          aria-selected={part === "players"}
          onClick={() => {
            noteClick("Игроки", "ок");
            setPart("players");
          }}
        >
          Игроки
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={part === "mods"}
          onClick={() => {
            noteClick("Моды RCON", "ок");
            setPart("mods");
          }}
        >
          Моды
        </button>
      </div>
      {part === "players" ? <RconPlayers data={data} /> : <RconMods data={data} />}
    </div>
  );
}

export function RconPlayers(props: { data: RconStatus | null }) {
  const data = props.data;
  if (!data || !data.up) return <p className="hint">нет связи</p>;
  if (!data.players_ok) return <pre className="logs">{data.players_raw || "не удалось получить"}</pre>;
  if (data.names.length === 0) return <p className="hint">нет</p>;
  return (
    <ul className="rcon-list">
      {data.names.map((name) => (
        <li key={name}>{name}</li>
      ))}
    </ul>
  );
}

export function RconMods(props: { data: RconStatus | null }) {
  const data = props.data;
  if (!data || !data.up) return <p className="hint">нет связи</p>;
  if (!data.mods_ok) return <p className="hint">не удалось получить</p>;
  if (data.mods.length === 0) {
    return data.mods_raw ? <pre className="logs">{data.mods_raw}</pre> : <p className="hint">нет</p>;
  }
  return (
    <ul className="rcon-list">
      {data.mods.map((mod) => (
        <li key={`${mod.id}:${mod.file}`}>
          {mod.id} <span className="ver">{mod.version}</span>
          {mod.file ? <span className="filename"> {mod.file}</span> : null}
        </li>
      ))}
    </ul>
  );
}
