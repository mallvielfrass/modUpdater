import type { RefObject } from "react";
import type { LogWindow } from "../../api";
import type { LogEvent } from "../logLines";

export function LogsTab(props: {
  logEvent: LogEvent;
  setLogEvent: (value: LogEvent) => void;
  logSince: LogWindow;
  onLogSince: (since: LogWindow) => void;
  logAuto: boolean;
  setLogAuto: (value: boolean) => void;
  loadLogs: (since: LogWindow) => void;
  logSaving: boolean;
  onDownloadLog: () => void;
  logBox: RefObject<HTMLPreElement>;
  shownLogs: string[];
}) {
  const { logEvent, setLogEvent, logSince, onLogSince, logAuto, setLogAuto, loadLogs, logSaving, onDownloadLog, logBox, shownLogs } = props;
  return (
        <section className="card log-panel server-log-panel">
          <div className="log-head">
            <h2 className="section-title">Лог</h2>
            <div className="log-tools">
              <select aria-label="События" value={logEvent} onChange={(event) => setLogEvent(event.target.value as LogEvent)}>
                <option value="all">все</option>
                <option value="error">ошибки</option>
                <option value="session">входы и выходы</option>
                <option value="chat">сообщения</option>
              </select>
              <select aria-label="Период лога" value={logSince} onChange={(event) => onLogSince(event.target.value as LogWindow)}>
                <option value="hour">Час</option>
                <option value="day">День</option>
                <option value="all">Всё</option>
              </select>
              <label className="check">
                <input type="checkbox" role="switch" checked={logAuto} onChange={(event) => setLogAuto(event.target.checked)} />
                автообновление
              </label>
              <button type="button" className="btn" onClick={() => void loadLogs(logSince)}>
                Обновить
              </button>
              <button type="button" className="btn" disabled={logSaving} onClick={() => void onDownloadLog()}>
                Скачать
              </button>
            </div>
          </div>
          <pre className="logs" ref={logBox}>{shownLogs.join("\n")}</pre>
        </section>
  );
}
