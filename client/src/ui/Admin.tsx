import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import { useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import {
  createInvite,
  createMod,
  checkServerS3,
  createServerBackup,
  createServerMilestone,
  deleteAdmin,
  deleteMod,
  deleteServerBackup,
  generateChangelog,
  type ChangelogDay,
  importModrinth,
  type Datapack,
  type ModrinthImport,
  restoreMod,
  revokeMod,
  launchServerBackup,
  rollbackServerBackup,
  saveBackupDest,
  saveBackupScope,
  type BackupDest,
  type BackupScope,
  saveDailyBackup,
  saveServerS3,
  serverBackupProgress,
  type ServerBackupProgress,
  deleteModConfig,
  deleteRelease,
  displayError,
  displayReleaseError,
  errorLog,
  failureText,
  fetchRcon,
  LOG_DOWNLOAD_LINES,
  type LogWindow,
  type RconStatus,
  listAdminMods,
  listAdmins,
  listDatapackFiles,
  listChangelog,
  listDatapacks,
  listServerBackups,
  listReleases,
  me,
  releaseClientVersion,
  restartServer,
  readDatapackFile,
  scanAdminMods,
  serverLogs,
  saveDatapackFile,
  updateMod,
  uploadDatapack,
  uploadModConfig,
} from "../api";
import { currentOs, downloadBackupZip, launchDevRelease, packDirZip, saveLogFile, uploadWorldFolder } from "../commands";
import { pickAsset } from "../semver";
import type { AdminReleases, AdminUser, ClientRelease, Mod, PendingRestart, ServerBackup } from "../types";
import { adminDebugSnapshot, noteClick, noteError, setDebugRedactions, subscribeAdminDebug } from "./debugLog";
import { filterLogLines, logFileName, type LogEvent } from "./logLines";
import { addedLabel } from "./UserMods";

type ModSort = "alphabet" | "installed";
type ModSide = "all" | "server" | "client" | "client_only";

interface Props {
  serverUrl: string;
  token: string;
  selfId: string;
  version: string;
  dev: boolean;
  onClose: () => void;
  onLogout: () => void;
  onSelfDeleted: () => void;
}

export function Admin(props: Props) {
  const [tab, setTab] = useState<"admins" | "mods" | "revoked" | "client" | "rcon" | "backups" | "changelog" | "logs" | "datapacks" | "debug">("admins");
  const [message, setMessage] = useState("");
  const [lines, setLines] = useState<string[] | null>(null);
  const [logSince, setLogSince] = useState<LogWindow>("hour");
  const [logEvent, setLogEvent] = useState<LogEvent>("all");
  const [logAuto, setLogAuto] = useState(true);
  const [logSaving, setLogSaving] = useState(false);
  const logBox = useRef<HTMLPreElement>(null);
  const [error, setError] = useState("");
  const [errorLogText, setErrorLogText] = useState("");
  const [admins, setAdmins] = useState<AdminUser[]>([]);
  const [mods, setMods] = useState<Mod[]>([]);
  const [lastRestart, setLastRestart] = useState("");
  const [pending, setPending] = useState<PendingRestart[]>([]);
  const [inviteUrl, setInviteUrl] = useState("");
  const [scanText, setScanText] = useState("");
  const [modQuery, setModQuery] = useState("");
  const [modSort, setModSort] = useState<ModSort>("alphabet");
  const [modSide, setModSide] = useState<ModSide>("all");
  const [tick, setTick] = useState(0);
  const [osName, setOsName] = useState("");
  const [serverVersion, setServerVersion] = useState<string | null>(null);
  const [backups, setBackups] = useState<ServerBackup[]>([]);
  const [backupBytes, setBackupBytes] = useState(0);
  const [freeBytes, setFreeBytes] = useState(0);
  const [backupBusy, setBackupBusy] = useState(false);
  const [backupJob, setBackupJob] = useState<BackupJob | null>(null);
  const backupStop = useRef(false);
  const backupTimer = useRef<number | null>(null);
  const [dailyTime, setDailyTime] = useState("");
  const [savedDaily, setSavedDaily] = useState("");
  const [backupScope, setBackupScope] = useState<BackupScope>("world");
  const [backupDest, setBackupDest] = useState<BackupDest>("disk");
  const [s3Endpoint, setS3Endpoint] = useState("");
  const [s3Region, setS3Region] = useState("");
  const [s3Bucket, setS3Bucket] = useState("");
  const [s3Prefix, setS3Prefix] = useState("");
  const [s3Access, setS3Access] = useState("");
  const [s3Secret, setS3Secret] = useState("");
  const [s3SecretSet, setS3SecretSet] = useState(false);
  const [s3Note, setS3Note] = useState("");

  useEffect(() => {
    void currentOs().then(setOsName).catch(() => setOsName(""));
  }, []);

  useEffect(() => {
    setDebugRedactions([s3Secret, s3Access]);
  }, [s3Secret, s3Access]);

  useEffect(() => {
    if (tab !== "logs") return;
    void loadLogs(logSince);
  }, [tab, logSince, props.serverUrl, props.token]);

  useEffect(() => {
    if (tab !== "logs" || !logAuto || lines === null) return;
    const timer = window.setInterval(() => {
      void serverLogs(props.serverUrl, props.token, logSince)
        .then((result) => setLines(result.lines))
        .catch((err) => report(err));
    }, 5000);
    return () => window.clearInterval(timer);
  }, [tab, logAuto, lines === null, logSince, props.serverUrl, props.token]);

  useLayoutEffect(() => {
    if (tab !== "logs") return;
    const el = logBox.current;
    if (!el) return;
    const fit = () => {
      const top = el.getBoundingClientRect().top;
      const card = el.parentElement;
      const cardStyle = card ? getComputedStyle(card) : null;
      const pad = cardStyle ? parseFloat(cardStyle.paddingBottom) || 0 : 0;
      const border = cardStyle ? parseFloat(cardStyle.borderBottomWidth) || 0 : 0;
      const app = el.closest(".app");
      const appPad = app ? parseFloat(getComputedStyle(app).paddingBottom) || 0 : 0;
      const next = Math.max(120, Math.floor(window.innerHeight - top - pad - border - appPad));
      const height = `${next}px`;
      if (el.style.height !== height) el.style.height = height;
    };
    fit();
    window.addEventListener("resize", fit);
    return () => window.removeEventListener("resize", fit);
  }, [tab, error, errorLogText]);

  useEffect(() => {
    let cancelled = false;
    void me(props.serverUrl, props.token)
      .then((result) => {
        if (!cancelled) setServerVersion(result.server_version);
      })
      .catch(() => {
        if (!cancelled) setServerVersion("");
      });
    return () => {
      cancelled = true;
    };
  }, [props.serverUrl, props.token]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const result = await listAdminMods(props.serverUrl, props.token);
        if (cancelled) return;
        setMods(result.mods);
        setLastRestart(result.last_restart_at);
        setPending(result.pending_restart);
      } catch (err) {
        if (!cancelled) report(err);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [props.serverUrl, props.token, tick]);

  useEffect(() => {
    let cancelled = false;
    void listServerBackups(props.serverUrl, props.token)
      .then((result) => {
        if (cancelled) return;
        setBackups(result.backups);
        setBackupBytes(result.backup_bytes);
        setFreeBytes(result.free_bytes);
        setDailyTime(result.daily_time);
        setSavedDaily(result.daily_time);
        setBackupScope(result.backup_scope);
        setBackupDest(result.s3.dest === "s3" ? "s3" : "disk");
        setS3Endpoint(result.s3.endpoint);
        setS3Region(result.s3.region);
        setS3Bucket(result.s3.bucket);
        setS3Prefix(result.s3.prefix);
        setS3Access(result.s3.access_key);
        setS3SecretSet(result.s3.secret_set);
      })
      .catch((err) => {
        if (!cancelled) report(err);
      });
    return () => {
      cancelled = true;
    };
  }, [props.serverUrl, props.token, tick]);

  useEffect(() => {
    if (tab !== "admins") return;
    let cancelled = false;
    void (async () => {
      try {
        const result = await listAdmins(props.serverUrl, props.token);
        if (!cancelled) setAdmins(result.admins);
      } catch (err) {
        if (!cancelled) report(err);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [tab, props.serverUrl, props.token, tick]);

  useEffect(() => () => {
    backupStop.current = true;
    if (backupTimer.current !== null) window.clearInterval(backupTimer.current);
  }, []);

  function reload() {
    setTick((value) => value + 1);
  }

  function report(err: unknown) {
    setError(displayError(err));
    setErrorLogText(errorLog(err));
  }

  function clearError() {
    setError("");
    setErrorLogText("");
  }

  function fail(control: string, err: unknown) {
    report(err);
    noteError(control, err);
  }

  function succeed(control: string, result = "ок") {
    noteClick(control, result);
  }

  function openTab(next: typeof tab, control: string) {
    noteClick(control, "ок");
    setTab(next);
  }

  async function onSaveDaily() {
    if (backupBusy) return;
    clearError();
    setBackupBusy(true);
    try {
      const result = await saveDailyBackup(props.serverUrl, props.token, dailyTime.trim());
      setDailyTime(result.time);
      setSavedDaily(result.time);
      succeed("Ежедневный бекап", "время сохранено");
    } catch (err) {
      fail("Ежедневный бекап", err);
    } finally {
      setBackupBusy(false);
    }
  }

  async function trackBackup(control: string, phase: string, work: () => Promise<void>) {
    if (backupBusy) return;
    clearError();
    setBackupBusy(true);
    setBackupJob({ phase, filesDone: 0, filesTotal: 0, bytesDone: 0, bytesTotal: 0 });
    let stop = false;
    backupTimer.current = window.setInterval(() => {
      void serverBackupProgress(props.serverUrl, props.token)
        .then((progress) => {
          if (!backupStop.current && !stop) {
            setBackups(progress.backups);
            setBackupBytes(progress.backup_bytes);
            setFreeBytes(progress.free_bytes);
          }
          if (backupStop.current || stop || !progress.running) return;
          setBackupJob(jobFromProgress(progress, phase));
        })
        .catch(() => { });
    }, 400);
    try {
      await work();
      succeed(control);
    } catch (err) {
      fail(control, err);
    } finally {
      stop = true;
      if (backupTimer.current !== null) {
        window.clearInterval(backupTimer.current);
        backupTimer.current = null;
      }
      setBackupBusy(false);
      setBackupJob(null);
    }
  }

  async function onBackupScope(scope: BackupScope) {
    const control = scope === "full" ? "полный бекап" : scope === "mods" ? "бекап модов и конфигов" : "бекап карты";
    if (backupBusy) return;
    if (scope === backupScope) {
      succeed(control, "уже выбрано");
      return;
    }
    clearError();
    setBackupBusy(true);
    try {
      const result = await saveBackupScope(props.serverUrl, props.token, scope);
      setBackupScope(result.scope);
      succeed(control);
    } catch (err) {
      fail(control, err);
    } finally {
      setBackupBusy(false);
    }
  }

  function s3Input() {
    return {
      endpoint: s3Endpoint.trim(),
      region: s3Region.trim(),
      bucket: s3Bucket.trim(),
      prefix: s3Prefix.trim(),
      access_key: s3Access.trim(),
      secret_key: s3Secret,
    };
  }

  async function onSaveS3() {
    if (backupBusy) return;
    clearError();
    setBackupBusy(true);
    setS3Note("");
    try {
      const result = await saveServerS3(props.serverUrl, props.token, s3Input());
      setS3Endpoint(result.s3.endpoint);
      setS3Region(result.s3.region);
      setS3Bucket(result.s3.bucket);
      setS3Prefix(result.s3.prefix);
      setS3Access(result.s3.access_key);
      setS3SecretSet(result.s3.secret_set);
      setS3Secret("");
      setBackupDest(result.s3.dest === "s3" ? "s3" : "disk");
      setS3Note("Сохранено.");
      succeed("S3 сохранить", "Сохранено.");
    } catch (err) {
      fail("S3 сохранить", err);
    } finally {
      setBackupBusy(false);
    }
  }

  async function onBackupDest(dest: BackupDest) {
    const control = dest === "s3" ? "s3" : "локально";
    if (backupBusy) return;
    if (dest === backupDest) {
      succeed(control, "уже выбрано");
      return;
    }
    clearError();
    setBackupBusy(true);
    try {
      const result = await saveBackupDest(props.serverUrl, props.token, dest);
      setBackupDest(result.dest);
      succeed(control);
    } catch (err) {
      fail(control, err);
    } finally {
      setBackupBusy(false);
    }
  }

  async function onCheckS3() {
    if (backupBusy) return;
    clearError();
    setBackupBusy(true);
    setS3Note("");
    try {
      const result = await checkServerS3(props.serverUrl, props.token, s3Input());
      setS3Note(result.message);
      succeed("S3 проверить", result.message);
    } catch (err) {
      fail("S3 проверить", err);
    } finally {
      setBackupBusy(false);
    }
  }

  async function onMilestone() {
    await trackBackup("Веха", "копирование", async () => {
      await createServerMilestone(props.serverUrl, props.token);
      reload();
    });
  }

  async function onBackup() {
    await trackBackup("Бекап", "копирование", async () => {
      await createServerBackup(props.serverUrl, props.token);
      reload();
    });
  }

  async function onDownload(item: ServerBackup) {
    if (backupBusy) return;
    clearError();
    setBackupBusy(true);
    setBackupJob({ phase: "скачивание", filesDone: 0, filesTotal: 0, bytesDone: 0, bytesTotal: 0 });
    try {
      await downloadBackupZip(props.serverUrl, props.token, item.id, backupZipName(item), (loaded, total) => {
        setBackupJob({ phase: "скачивание", filesDone: 0, filesTotal: 0, bytesDone: loaded, bytesTotal: total });
      });
      succeed("Скачать");
    } catch (err) {
      fail("Скачать", err);
    } finally {
      setBackupBusy(false);
      setBackupJob(null);
    }
  }

  async function onUploadWorld() {
    if (backupBusy) return;
    clearError();
    setBackupBusy(true);
    setBackupJob({ phase: "загрузка", filesDone: 0, filesTotal: 0, bytesDone: 0, bytesTotal: 0 });
    try {
      const started = await uploadWorldFolder(props.serverUrl, props.token, (loaded, total) => {
        setBackupJob({ phase: "загрузка", filesDone: 0, filesTotal: 0, bytesDone: loaded, bytesTotal: total });
      });
      if (started) {
        succeed("Загрузить мир");
        reload();
      } else {
        succeed("Загрузить мир", "отменено");
      }
    } catch (err) {
      fail("Загрузить мир", err);
    } finally {
      setBackupBusy(false);
      setBackupJob(null);
    }
  }

  async function onLaunch(id: string) {
    await trackBackup("Запустить", "запуск", async () => {
      const result = await launchServerBackup(props.serverUrl, props.token, id);
      setBackups(result.backups);
      setLastRestart(result.last_restart_at);
      setPending(result.pending_restart);
      reload();
    });
  }

  async function onDeleteBackup(id: string) {
    if (backupBusy) return;
    clearError();
    setBackupBusy(true);
    try {
      const result = await deleteServerBackup(props.serverUrl, props.token, id);
      setBackups(result.backups);
      succeed("Удалить бекап");
    } catch (err) {
      fail("Удалить бекап", err);
    } finally {
      setBackupBusy(false);
    }
  }

  async function onRollback(id: string) {
    await trackBackup("Развернуть", "разворот", async () => {
      const result = await rollbackServerBackup(props.serverUrl, props.token, id);
      setBackups(result.backups);
      setLastRestart(result.last_restart_at);
      setPending(result.pending_restart);
      reload();
    });
  }

  async function onRestart() {
    clearError();
    try {
      const result = await restartServer(props.serverUrl, props.token, message.trim());
      setLastRestart(result.last_restart_at);
      setPending(result.pending_restart);
      if (result.backup_error) {
        setError(result.backup_error);
        noteClick("Перезапуск", result.backup_error);
      } else {
        succeed("Перезапуск");
      }
      reload();
    } catch (err) {
      fail("Перезапуск", err);
    }
  }

  async function loadLogs(since: LogWindow) {
    clearError();
    try {
      const result = await serverLogs(props.serverUrl, props.token, since);
      setLines(result.lines);
    } catch (err) {
      report(err);
    }
  }

  function onLogSince(since: LogWindow) {
    setLogSince(since);
  }

  async function onDownloadLog() {
    if (logSaving) return;
    setLogSaving(true);
    clearError();
    try {
      const result = await serverLogs(props.serverUrl, props.token, logSince, LOG_DOWNLOAD_LINES);
      const filename = logFileName(logSince, new Date());
      await new Promise<void>((resolve) => {
        window.setTimeout(resolve, 0);
      });
      await saveLogFile(filename, (result.lines ?? []).join("\n"));
    } catch (err) {
      report(err);
    } finally {
      setLogSaving(false);
    }
  }

  async function onInvite() {
    clearError();
    try {
      const result = await createInvite(props.serverUrl, props.token, 72);
      setInviteUrl(result.invite.url);
      succeed("Приглашение");
    } catch (err) {
      fail("Приглашение", err);
    }
  }

  async function onDeleteAdmin(id: string) {
    clearError();
    try {
      await deleteAdmin(props.serverUrl, props.token, id);
      succeed("Удалить админа");
      if (id === props.selfId) {
        props.onSelfDeleted();
        return;
      }
      reload();
    } catch (err) {
      fail("Удалить админа", err);
    }
  }

  async function onScan() {
    clearError();
    try {
      const result = await scanAdminMods(props.serverUrl, props.token);
      const conflicts = result.conflicts.length > 0 ? ` ${result.conflicts.join(", ")}` : "";
      const text = `добавлено ${result.added}, обновлено ${result.updated}, удалено ${result.removed}${conflicts}`;
      setScanText(text);
      succeed("Сканировать", text);
      reload();
    } catch (err) {
      fail("Сканировать", err);
    }
  }

  const pendingNames = pending.map((item) => item.name || item.filename).filter((name) => name !== "");
  const currentBackup = backups.find((item) => item.current);
  const activeMods = listActiveMods(mods, modQuery, modSide, modSort);
  const revokedMods = mods.filter((mod) => mod.revoked).sort(compareMods);
  const shownLogs = filterLogLines(lines ?? [], logEvent);

  return (
    <section>
      <header className="appbar">
        <div className="brand">
          modupdater
          {props.version ? <span className="ver">v{props.version}</span> : null}
          <span className="ver">
            {serverVersion === null ? "сервер: …" : serverVersion ? `сервер v${serverVersion}` : "сервер: не отдаёт"}
          </span>
          {props.dev ? <span className="badge">dev</span> : null}
        </div>
        <div className="row">
          <button
            type="button"
            className="btn-text"
            onClick={() => {
              noteClick("Закрыть", "ок");
              props.onClose();
            }}
          >
            Закрыть
          </button>
          <button
            type="button"
            className="btn-text"
            onClick={() => {
              noteClick("Выйти", "ок");
              props.onLogout();
            }}
          >
            Выйти
          </button>
        </div>
      </header>
      <div className="restart-meta">
        <p className="hint">Последний перезапуск: {lastRestart || "не было"}</p>
        <p className="hint">Ожидают перезапуска: {pendingNames.length > 0 ? pendingNames.join(", ") : "нет"}</p>
      </div>
      <div className="serverbar">
        <div className="field">
          <label className="field-label" htmlFor="restart-message">
            Сообщение
          </label>
          <input id="restart-message" value={message} onChange={(event) => setMessage(event.target.value)} />
        </div>
        <button type="button" className="btn btn-primary" onClick={() => void onRestart()}>
          Перезапуск
        </button>
      </div>
      <ErrorNote message={error} log={errorLogText} />
      <div className="tabs" role="tablist">

        <button type="button" role="tab" aria-selected={tab === "mods"} onClick={() => openTab("mods", "Моды")}>
          Моды
        </button>
        <button type="button" role="tab" aria-selected={tab === "revoked"} onClick={() => openTab("revoked", "Отозванные моды")}>
          Отозванные моды
        </button>
        <button type="button" role="tab" aria-selected={tab === "client"} onClick={() => openTab("client", "Клиент")}>
          Клиент
        </button>
        <button type="button" role="tab" aria-selected={tab === "rcon"} onClick={() => openTab("rcon", "RCON")}>
          RCON
        </button>
        <button type="button" role="tab" aria-selected={tab === "backups"} onClick={() => openTab("backups", "Бекапы")}>
          Бекапы
        </button>
        <button type="button" role="tab" aria-selected={tab === "changelog"} onClick={() => openTab("changelog", "Чейнжлог")}>
          Чейнжлог
        </button>
        <button type="button" role="tab" aria-selected={tab === "datapacks"} onClick={() => openTab("datapacks", "Датапаки")}>
          Датапаки
        </button>
        <button type="button" role="tab" aria-selected={tab === "admins"} onClick={() => openTab("admins", "Админы")}>
          Админы
        </button>
        <button type="button" role="tab" aria-selected={tab === "logs"} onClick={() => openTab("logs", "Логи")}>
          Логи
        </button>
        <button type="button" role="tab" aria-selected={tab === "debug"} onClick={() => openTab("debug", "Debug")}>
          Debug
        </button>
      </div>
      {tab === "admins" ? (
        <div className="stack">
          <div className="toolbar">
            <button type="button" className="btn btn-primary" onClick={() => void onInvite()}>
              Приглашение
            </button>
          </div>
          {inviteUrl ? (
            <label className="field">
              <span className="field-label">Ссылка для приглашения</span>
              <input className="invite" readOnly value={inviteUrl} onFocus={(event) => event.currentTarget.select()} />
            </label>
          ) : null}
          {admins.map((admin) => (
            <article className="card mod-card" key={admin.id}>
              <div className="mod-title">
                <span>{admin.username}</span>
                {admin.root ? <span className="badge">корень</span> : null}
              </div>
              {admin.root ? null : (
                <button type="button" className="btn btn-quiet" onClick={() => void onDeleteAdmin(admin.id)}>
                  Удалить
                </button>
              )}
            </article>
          ))}
        </div>
      ) : tab === "mods" ? (
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
      ) : tab === "revoked" ? (
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
      ) : tab === "client" ? (
        <ClientTab serverUrl={props.serverUrl} token={props.token} osName={osName} />
      ) : tab === "rcon" ? (
        <RconTab serverUrl={props.serverUrl} token={props.token} onError={report} />
      ) : tab === "logs" ? (
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
      ) : tab === "changelog" ? (
        <ChangelogTab serverUrl={props.serverUrl} token={props.token} onSaved={clearError} onError={report} />
      ) : tab === "datapacks" ? (
        <DatapacksTab serverUrl={props.serverUrl} token={props.token} onSaved={clearError} onError={report} />
      ) : tab === "debug" ? (
        <DebugTab />
      ) : (
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
      )}
    </section>
  );
}

interface BackupJob {
  phase: string;
  filesDone: number;
  filesTotal: number;
  bytesDone: number;
  bytesTotal: number;
}

function jobFromProgress(progress: ServerBackupProgress, fallback: string): BackupJob {
  return {
    phase: progress.phase || fallback,
    filesDone: progress.files_done,
    filesTotal: progress.files_total,
    bytesDone: progress.bytes_done,
    bytesTotal: progress.bytes_total,
  };
}

function ServerBackupMeter(props: { job: BackupJob }) {
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

function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "0 Б";
  if (n < 1024) return `${Math.round(n)} Б`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} КБ`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} МБ`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(1)} ГБ`;
}

function backupKindText(kind?: string): string {
  if (kind === "milestone") return "веха";
  if (kind === "snapshot") return "снапшот";
  return "";
}

function backupScopeText(scope?: string): string {
  if (scope === "world") return "бекап карты";
  if (scope === "full") return "полный бекап";
  if (scope === "mods") return "бекап модов и конфигов";
  return "";
}

function backupZipName(item: ServerBackup): string {
  const digits = (item.created_at || "").replace(/\D/g, "");
  return `backup-${digits || "backup"}-${item.id}.tar.zst`;
}

function backupWhen(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(date.getDate())}.${p(date.getMonth() + 1)}.${date.getFullYear()} ${p(date.getHours())}:${p(date.getMinutes())}`;
}

function serverStatus(version: string | null, current: ServerBackup | undefined): string {
  const build = version === null ? "…" : version ? `v${version}` : "не отдаёт";
  if (!current) return `Версия сервера: ${build} · бекап нет`;
  return `Версия сервера: ${build} · бекап сейчас ${backupWhen(current.created_at)}`;
}

function ErrorNote(props: { message: string; log: string }) {
  if (!props.message) return null;
  return (
    <div>
      <p className="error">{props.message}</p>
      {props.log ? (
        <details className="error-log">
          <summary>лог</summary>
          <pre>{props.log}</pre>
        </details>
      ) : null}
    </div>
  );
}

function AddMod(props: {
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

function ImportModrinth(props: {
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

function ImportReport(props: { result: ModrinthImport }) {
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

function compareMods(a: Mod, b: Mod): number {
  const byName = a.name.localeCompare(b.name, "ru", { sensitivity: "base" });
  if (byName !== 0) return byName;
  return a.filename.localeCompare(b.filename, "ru", { sensitivity: "base" });
}

function compareInstalled(a: Mod, b: Mod): number {
  const byDate = b.created_at.localeCompare(a.created_at);
  if (byDate !== 0) return byDate;
  return compareMods(a, b);
}

function listActiveMods(mods: Mod[], query: string, side: ModSide, sort: ModSort): Mod[] {
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

function RevokedRow(props: {
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

function ModRow(props: {
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

function changelogDayLabel(day: string): string {
  const parts = day.split("-");
  if (parts.length !== 3) return day;
  return `${parts[2]}.${parts[1]}.${parts[0]}`;
}

function ChangelogTab(props: {
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

function DatapacksTab(props: {
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

function DatapackCard(props: {
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

function RconTab(props: { serverUrl: string; token: string; onError: (err: unknown) => void }) {
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

function RconPlayers(props: { data: RconStatus | null }) {
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

function RconMods(props: { data: RconStatus | null }) {
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

function ClientTab(props: { serverUrl: string; token: string; osName: string }) {
  const [data, setData] = useState<AdminReleases | null>(null);
  const [error, setError] = useState("");
  const [errorLogText, setErrorLogText] = useState("");
  const [busy, setBusy] = useState("");
  const [percent, setPercent] = useState(0);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const result = await listReleases(props.serverUrl, props.token);
        if (!cancelled) setData(result);
      } catch (err) {
        if (!cancelled) {
          setError(displayReleaseError(err));
          setErrorLogText(errorLog(err));
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [props.serverUrl, props.token, tick]);

  const devs = (data?.releases ?? []).filter((rel) => rel.status === "dev");

  async function launch(rel: ClientRelease) {
    const asset = pickAsset(props.osName, rel.linux_sha256, rel.linux_size, rel.windows_sha256, rel.windows_size);
    if (!asset || busy) return;
    setBusy(rel.version);
    setError("");
    setErrorLogText("");
    setPercent(0);
    let unlisten: UnlistenFn | null = null;
    try {
      unlisten = await listen<{ loaded: number; total: number }>("update-progress", (event) => {
        const total = event.payload.total;
        const pct = total > 0 ? Math.round((event.payload.loaded / total) * 100) : 0;
        setPercent(Math.min(100, pct));
      });
      await launchDevRelease(rel.version, asset.sha256, props.token);
      noteClick("Запустить", "ок");
    } catch (err) {
      setError(displayReleaseError(err));
      setErrorLogText(errorLog(err));
      noteClick("Запустить", clientFailure(err));
    } finally {
      unlisten?.();
      setBusy("");
      setPercent(0);
    }
  }

  async function publish(version: string) {
    if (busy) return;
    setBusy(version);
    setError("");
    setErrorLogText("");
    try {
      await releaseClientVersion(props.serverUrl, props.token, version);
      noteClick("Релиз", "ок");
      setTick((value) => value + 1);
    } catch (err) {
      setError(displayReleaseError(err));
      setErrorLogText(errorLog(err));
      noteClick("Релиз", clientFailure(err));
    } finally {
      setBusy("");
    }
  }

  async function reject(version: string) {
    if (busy) return;
    setBusy(version);
    setError("");
    setErrorLogText("");
    try {
      await deleteRelease(props.serverUrl, props.token, version);
      noteClick("Отклонить", "ок");
      setTick((value) => value + 1);
    } catch (err) {
      setError(displayReleaseError(err));
      setErrorLogText(errorLog(err));
      noteClick("Отклонить", clientFailure(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="stack">
      <p className="hint">{data?.active_version ? `У игроков: v${data.active_version}` : "релиза нет"}</p>
      <ErrorNote message={error} log={errorLogText} />
      {devs.length === 0 ? <p className="hint">dev-сборок нет</p> : null}
      {devs.map((rel) => {
        const asset = pickAsset(props.osName, rel.linux_sha256, rel.linux_size, rel.windows_sha256, rel.windows_size);
        const both = rel.linux_sha256 !== "" && rel.windows_sha256 !== "";
        return (
          <article className="card" key={rel.version}>
            <div className="mod-title">
              <span>v{rel.version}</span>
            </div>
            <p className="hint" title={rel.linux_sha256}>
              linux: {rel.linux_sha256 ? `есть ${rel.linux_sha256.slice(0, 12)}` : "нет"}
            </p>
            <p className="hint" title={rel.windows_sha256}>
              windows: {rel.windows_sha256 ? `есть ${rel.windows_sha256.slice(0, 12)}` : "нет"}
            </p>
            <div className="row">
              <button type="button" className="btn" disabled={!asset || busy !== ""} onClick={() => void launch(rel)}>
                Запустить
              </button>
              <button type="button" className="btn btn-primary" disabled={!both || busy !== ""} onClick={() => void publish(rel.version)}>
                Релиз
              </button>
              <button type="button" className="btn btn-quiet" disabled={busy !== ""} onClick={() => void reject(rel.version)}>
                Отклонить
              </button>
            </div>
            {busy === rel.version && percent > 0 ? <span className="progress">{percent}%</span> : null}
          </article>
        );
      })}
    </div>
  );
}

function DebugTab() {
  const rows = useSyncExternalStore(subscribeAdminDebug, adminDebugSnapshot, adminDebugSnapshot);
  return (
    <section className="card log-panel">
      <h2 className="section-title">Debug</h2>
      {rows.length === 0 ? (
        <p className="hint">Пока пусто.</p>
      ) : (
        <pre className="logs">{rows.map((row) => `${row.at}  ${row.control}  ${row.result}`).join("\n")}</pre>
      )}
    </section>
  );
}

function clientFailure(err: unknown): string {
  const shown = failureText(err);
  if (shown.startsWith("внутренняя ошибка сервера")) return shown;
  return displayReleaseError(err);
}

function FileButton(props: { file: File | null; disabled?: boolean; onFile: (file: File | null) => void }) {
  return (
    <label className={`btn file-btn${props.disabled ? " is-disabled" : ""}`}>
      {props.file ? props.file.name : "Файл"}
      <input
        type="file"
        accept=".jar,application/java-archive"
        disabled={props.disabled}
        onChange={(event) => {
          props.onFile(event.target.files?.[0] ?? null);
          event.currentTarget.value = "";
        }}
      />
    </label>
  );
}
