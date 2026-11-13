import { useEffect, useState } from "react";
import { generateChangelog, listChangelog, type ChangelogDay } from "../../api";
import { noteClick, noteError } from "../debugLog";

export function changelogDayLabel(day: string): string {
  const parts = day.split("-");
  if (parts.length !== 3) return day;
  return `${parts[2]}.${parts[1]}.${parts[0]}`;
}

export function ChangelogTab(props: {
  serverUrl: string;
  token: string;
  onSaved: () => void;
  onError: (err: unknown) => void;
}) {
  const [days, setDays] = useState<ChangelogDay[]>([]);
  const [busy, setBusy] = useState(false);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let cancelled = false;
    void listChangelog(props.serverUrl, props.token)
      .then((result) => {
        if (!cancelled) setDays(result.days);
      })
      .catch((err) => {
        if (!cancelled) props.onError(err);
      });
    return () => {
      cancelled = true;
    };
  }, [props.serverUrl, props.token, tick]);

  async function generate() {
    if (busy) return;
    setBusy(true);
    try {
      const result = await generateChangelog(props.serverUrl, props.token);
      setDays(result.days);
      noteClick("Сгенерировать чейнжлог", "ок");
      props.onSaved();
      setTick((value) => value + 1);
    } catch (err) {
      noteError("Сгенерировать чейнжлог", err);
      props.onError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="stack">
      <div className="toolbar">
        <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void generate()}>
          Сгенерировать
        </button>
      </div>
      {days.length === 0 ? <p className="hint">Нет изменений</p> : null}
      {days.map((day) => (
        <article className="card changelog-card" key={day.day}>
          <div className="mod-title">
            <span>{changelogDayLabel(day.day)}</span>
          </div>
          <ul className="changelog-lines">
            {day.lines.map((line, index) => (
              <li key={`${day.day}-${index}`}>{line}</li>
            ))}
          </ul>
        </article>
      ))}
    </div>
  );
}
