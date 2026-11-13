import { deleteMod, restoreMod } from "../../api";
import type { Mod } from "../../types";
import { noteClick, noteError } from "../debugLog";
import { addedLabel } from "../UserMods";

export function RevokedRow(props: {
  mod: Mod;
  serverUrl: string;
  token: string;
  onSaved: () => void;
  onError: (err: unknown) => void;
}) {
  const added = addedLabel(props.mod.created_at);

  async function restore() {
    try {
      await restoreMod(props.serverUrl, props.token, props.mod.id);
      noteClick("Вернуть", "ок");
      props.onSaved();
    } catch (err) {
      noteError("Вернуть", err);
      props.onError(err);
    }
  }

  async function remove() {
    try {
      await deleteMod(props.serverUrl, props.token, props.mod.id);
      noteClick("Удалить", "ок");
      props.onSaved();
    } catch (err) {
      noteError("Удалить", err);
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
          <span className="badge">отозван</span>
        </div>
        <button type="button" className="btn btn-quiet" onClick={() => void restore()}>
          Вернуть
        </button>
        <button type="button" className="btn btn-quiet" onClick={() => void remove()}>
          Удалить
        </button>
      </div>
    </article>
  );
}

export function RevokedTab(props: {
  serverUrl: string;
  token: string;
  revokedMods: Mod[];
  clearError: () => void;
  reload: () => void;
  report: (err: unknown) => void;
}) {
  const { revokedMods, clearError, reload, report } = props;
  return (
        <div className="stack">
          {revokedMods.length === 0 ? <p className="hint">Список пуст.</p> : null}
          {revokedMods.map((mod) => (
            <RevokedRow
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
