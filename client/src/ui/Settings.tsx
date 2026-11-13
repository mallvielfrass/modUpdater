import { pickModsDir } from "../commands";

interface Props {
  modsDir: string;
  error: string;
  busy: boolean;
  onModsDir: (value: string) => void;
  onPick: (dir: string) => void;
  onCheck: () => void;
}

export function Settings(props: Props) {
  async function browse() {
    const picked = await pickModsDir();
    if (picked) props.onPick(picked);
  }

  const blocked = props.modsDir.trim() === "";

  return (
    <section className="card connect">
      <div className="field">
        <label className="field-label" htmlFor="mods-dir">
          Папка модов
        </label>
        <span className="row">
          <input id="mods-dir" value={props.modsDir} onChange={(event) => props.onModsDir(event.target.value)} />
          <button type="button" className="icon-btn" onClick={() => void browse()} aria-label="Папка модов">
            <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true">
              <path d="M3 7h6l2 2h10v10H3V7z" fill="none" stroke="currentColor" strokeWidth="2" />
            </svg>
          </button>
        </span>
      </div>
      <div className="connect-actions">
        <button type="button" className="btn btn-primary" onClick={props.onCheck} disabled={props.busy || blocked}>
          Проверить
        </button>
      </div>
      {props.error ? <p className="error">{props.error}</p> : null}
    </section>
  );
}
