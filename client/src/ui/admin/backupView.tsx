import type { ServerBackup } from "../../types";
import type { ServerBackupProgress } from "../../api";

export interface BackupJob {
  phase: string;
  filesDone: number;
  filesTotal: number;
  bytesDone: number;
  bytesTotal: number;
}

export function jobFromProgress(progress: ServerBackupProgress, fallback: string): BackupJob {
  return {
    phase: progress.phase || fallback,
    filesDone: progress.files_done,
    filesTotal: progress.files_total,
    bytesDone: progress.bytes_done,
    bytesTotal: progress.bytes_total,
  };
}

export function ServerBackupMeter(props: { job: BackupJob }) {
  const job = props.job;
  const transfer = job.phase === "скачивание" || job.phase === "загрузка";
  const byBytes = job.bytesTotal > 0;
  const known = byBytes || job.filesTotal > 0;
  const ratio = byBytes ? job.bytesDone / job.bytesTotal : job.filesTotal > 0 ? job.filesDone / job.filesTotal : 0;
  const width = Math.max(0, Math.min(100, ratio * 100));
  const detail = job.filesTotal > 0 ? `${job.filesDone} / ${job.filesTotal}` : byBytes ? `${job.bytesDone} / ${job.bytesTotal}` : "";
  const doneText = formatBytes(job.bytesDone);
  const left = Math.max(0, job.bytesTotal - job.bytesDone);
  const text = transfer
    ? byBytes
      ? `${job.phase} · загружено ${doneText}, осталось ${formatBytes(left)}`
      : `${job.phase} · загружено ${doneText}`
    : detail
      ? `${job.phase} · ${detail}`
      : job.phase;
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

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "0 Б";
  if (n < 1024) return `${Math.round(n)} Б`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} КБ`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} МБ`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(1)} ГБ`;
}

export function backupKindText(kind?: string): string {
  if (kind === "milestone") return "веха";
  if (kind === "snapshot") return "снапшот";
  return "";
}

export function backupScopeText(scope?: string): string {
  if (scope === "world") return "бекап карты";
  if (scope === "full") return "полный бекап";
  if (scope === "mods") return "бекап модов и конфигов";
  return "";
}

export function backupZipName(item: ServerBackup): string {
  const digits = (item.created_at || "").replace(/\D/g, "");
  return `backup-${digits || "backup"}-${item.id}.tar.zst`;
}

export function backupWhen(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(date.getDate())}.${p(date.getMonth() + 1)}.${date.getFullYear()} ${p(date.getHours())}:${p(date.getMinutes())}`;
}

export function serverStatus(version: string | null, current: ServerBackup | undefined): string {
  const build = version === null ? "…" : version ? `v${version}` : "не отдаёт";
  if (!current) return `Версия сервера: ${build} · бекап нет`;
  return `Версия сервера: ${build} · бекап сейчас ${backupWhen(current.created_at)}`;
}

import type { BackupDest, BackupScope } from "../../api";

export function BackupsTab(props: {
  serverVersion: string | null;
  currentBackup: ServerBackup | undefined;
  backupBytes: number;
  freeBytes: number;
  backupBusy: boolean;
  dailyTime: string;
  setDailyTime: (value: string) => void;
  onSaveDaily: () => void;
  savedDaily: string;
  backupScope: BackupScope;
  onBackupScope: (scope: BackupScope) => void;
  backupDest: BackupDest;
  onBackupDest: (dest: BackupDest) => void;
  s3Endpoint: string;
  setS3Endpoint: (value: string) => void;
  s3Region: string;
  setS3Region: (value: string) => void;
  s3Bucket: string;
  setS3Bucket: (value: string) => void;
  s3Prefix: string;
  setS3Prefix: (value: string) => void;
  s3Access: string;
  setS3Access: (value: string) => void;
  s3Secret: string;
  setS3Secret: (value: string) => void;
  s3SecretSet: boolean;
  onSaveS3: () => void;
  onCheckS3: () => void;
  s3Note: string;
  onMilestone: () => void;
  onBackup: () => void;
  onUploadWorld: () => void;
  backupJob: BackupJob | null;
  backups: ServerBackup[];
  onRollback: (id: string) => void;
  onLaunch: (id: string) => void;
  onDownload: (item: ServerBackup) => void;
  onDeleteBackup: (id: string) => void;
}) {
  const {
    serverVersion, currentBackup, backupBytes, freeBytes, backupBusy, dailyTime, setDailyTime, onSaveDaily, savedDaily,
    backupScope, onBackupScope, backupDest, onBackupDest, s3Endpoint, setS3Endpoint, s3Region, setS3Region, s3Bucket, setS3Bucket,
    s3Prefix, setS3Prefix, s3Access, setS3Access, s3Secret, setS3Secret, s3SecretSet, onSaveS3, onCheckS3, s3Note,
    onMilestone, onBackup, onUploadWorld, backupJob, backups, onRollback, onLaunch, onDownload, onDeleteBackup,
  } = props;
  return (
        <div className="stack">
          <p className="hint">{serverStatus(serverVersion, currentBackup)}</p>
          <p className="hint">{`бекапы: ${formatBytes(backupBytes)} · свободно: ${formatBytes(freeBytes)}`}</p>
          <div className="field">
            <label className="field-label" htmlFor="daily-backup">
              Ежедневный бекап
            </label>
            <div className="row">
              <input id="daily-backup" type="time" step={60} value={dailyTime} onChange={(event) => setDailyTime(event.target.value)} />
              <button type="button" className="btn" disabled={backupBusy} onClick={() => void onSaveDaily()}>
                Сохранить
              </button>
              <span className="hint">Время сохранения: {savedDaily || "выключен"}</span>
            </div>
          </div>
          <div className="field">
            <span className="field-label" id="backup-scope-label">
              Область бекапа
            </span>
            <div className="scope" role="radiogroup" aria-labelledby="backup-scope-label">
              <button
                type="button"
                role="radio"
                aria-checked={backupScope === "world"}
                className={backupScope === "world" ? "btn btn-primary" : "btn"}
                disabled={backupBusy}
                onClick={() => void onBackupScope("world")}
              >
                бекап карты
              </button>
              <button
                type="button"
                role="radio"
                aria-checked={backupScope === "full"}
                className={backupScope === "full" ? "btn btn-primary" : "btn"}
                disabled={backupBusy}
                onClick={() => void onBackupScope("full")}
              >
                полный бекап
              </button>
              <button
                type="button"
                role="radio"
                aria-checked={backupScope === "mods"}
                className={backupScope === "mods" ? "btn btn-primary" : "btn"}
                disabled={backupBusy}
                onClick={() => void onBackupScope("mods")}
              >
                бекап модов и конфигов
              </button>
            </div>
          </div>
          <div className="field">
            <span className="field-label">S3</span>
            <div className="row">
              <div className="scope" role="radiogroup" aria-label="Куда сохранять">
                <button
                  type="button"
                  role="radio"
                  aria-checked={backupDest !== "s3"}
                  className={backupDest !== "s3" ? "btn btn-primary" : "btn"}
                  disabled={backupBusy}
                  onClick={() => void onBackupDest("disk")}
                >
                  локально
                </button>
                <button
                  type="button"
                  role="radio"
                  aria-checked={backupDest === "s3"}
                  className={backupDest === "s3" ? "btn btn-primary" : "btn"}
                  disabled={backupBusy}
                  onClick={() => void onBackupDest("s3")}
                >
                  s3
                </button>
              </div>
              <input aria-label="endpoint" placeholder="endpoint" value={s3Endpoint} onChange={(event) => setS3Endpoint(event.target.value)} />
              <input aria-label="регион" placeholder="регион" value={s3Region} onChange={(event) => setS3Region(event.target.value)} />
              <input aria-label="бакет" placeholder="бакет" value={s3Bucket} onChange={(event) => setS3Bucket(event.target.value)} />
              <input aria-label="префикс" placeholder="префикс" value={s3Prefix} onChange={(event) => setS3Prefix(event.target.value)} />
              <input aria-label="ключ доступа" placeholder="ключ доступа" value={s3Access} onChange={(event) => setS3Access(event.target.value)} autoComplete="off" />
              <input aria-label="секрет" placeholder={s3SecretSet ? "секрет сохранён" : "секрет"} type="password" value={s3Secret} onChange={(event) => setS3Secret(event.target.value)} autoComplete="new-password" />
              <button type="button" className="btn" disabled={backupBusy} onClick={() => void onSaveS3()}>
                Сохранить
              </button>
              <button type="button" className="btn" disabled={backupBusy} onClick={() => void onCheckS3()}>
                Проверить
              </button>
            </div>
            {s3Note ? <span className="hint">{s3Note}</span> : null}
          </div>
          <div className="row">
            <button type="button" className="btn" disabled={backupBusy} onClick={() => void onMilestone()}>
              Веха
            </button>
            <button type="button" className="btn" disabled={backupBusy} onClick={() => void onBackup()}>
              Бекап
            </button>
            <button type="button" className="btn" disabled={backupBusy} onClick={() => void onUploadWorld()}>
              Загрузить мир
            </button>
          </div>
          {backupJob ? <ServerBackupMeter job={backupJob} /> : null}
          {backups.map((item) => (
            <article className="card mod-card" key={item.id}>
              <div className="mod-title backup-line">
                <span>{backupWhen(item.created_at)}</span>
                {backupKindText(item.kind) ? <span className="hint">{backupKindText(item.kind)}</span> : null}
                {backupScopeText(item.scope) ? <span className="hint">{backupScopeText(item.scope)}</span> : null}
                {item.status === "running" ? <span className="hint">копируется</span> : null}
                {item.status === "failed" ? <span className="error">{item.error || "Не удалось сделать бекап."}</span> : null}
                <span className="hint">{formatBytes(item.size ?? 0)}</span>
                {item.sha256 ? <span className="backup-sha">{item.sha256}</span> : null}
                <span className="hint">{item.storage === "s3" ? "на s3" : "на диске"}</span>
                {item.label && item.label !== backupKindText(item.kind) ? <span className="hint">{item.label}</span> : null}
                {item.current ? <span className="badge">сейчас</span> : null}
              </div>
              <div className="row">
                {item.current ? null : (
                  <button type="button" className="btn btn-quiet" disabled={backupBusy} onClick={() => void onRollback(item.id)}>
                    Развернуть
                  </button>
                )}
                <button type="button" className="btn btn-quiet" disabled={backupBusy} onClick={() => void onLaunch(item.id)}>
                  Запустить
                </button>
                <button type="button" className="btn btn-quiet" disabled={backupBusy} onClick={() => void onDownload(item)}>
                  Скачать
                </button>
                <button type="button" className="btn btn-quiet" disabled={backupBusy} onClick={() => void onDeleteBackup(item.id)}>
                  Удалить
                </button>
              </div>
            </article>
          ))}
        </div>
  );
}
