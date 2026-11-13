import { useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  checkServerS3,
  createInvite,
  createServerBackup,
  createServerMilestone,
  deleteAdmin,
  deleteServerBackup,
  displayError,
  errorLog,
  launchServerBackup,
  listAdminMods,
  listAdmins,
  listServerBackups,
  LOG_DOWNLOAD_LINES,
  type LogWindow,
  me,
  restartServer,
  rollbackServerBackup,
  saveBackupDest,
  saveBackupScope,
  saveDailyBackup,
  saveServerS3,
  scanAdminMods,
  serverBackupProgress,
  serverLogs,
  type BackupDest,
  type BackupScope,
} from "../api";
import { currentOs, downloadBackupZip, saveLogFile, uploadWorldFolder } from "../commands";
import type { AdminUser, Mod, PendingRestart, ServerBackup } from "../types";
import { AdminsTab } from "./admin/AdminsTab";
import { BackupsTab, backupZipName, jobFromProgress, type BackupJob } from "./admin/backupView";
import { ChangelogTab } from "./admin/ChangelogTab";
import { ClientTab } from "./admin/ClientTab";
import { DatapacksTab } from "./admin/DatapacksTab";
import { DebugTab } from "./admin/DebugTab";
import { ErrorNote } from "./admin/ErrorNote";
import { LogsTab } from "./admin/LogsTab";
import { compareMods, listActiveMods, ModsTab, type ModSide, type ModSort } from "./admin/modsTab";
import { RconTab } from "./admin/RconTab";
import { RevokedTab } from "./admin/RevokedTab";
import { noteClick, noteError, setDebugRedactions } from "./debugLog";
import { filterLogLines, logFileName, type LogEvent } from "./logLines";

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
        <AdminsTab inviteUrl={inviteUrl} admins={admins} onInvite={() => void onInvite()} onDeleteAdmin={(id) => void onDeleteAdmin(id)} />
      ) : tab === "mods" ? (
        <ModsTab
          serverUrl={props.serverUrl}
          token={props.token}
          scanText={scanText}
          onScan={() => void onScan()}
          modQuery={modQuery}
          setModQuery={setModQuery}
          modSort={modSort}
          setModSort={setModSort}
          modSide={modSide}
          setModSide={setModSide}
          activeMods={activeMods}
          clearError={clearError}
          reload={reload}
          report={report}
        />
      ) : tab === "revoked" ? (
        <RevokedTab
          serverUrl={props.serverUrl}
          token={props.token}
          revokedMods={revokedMods}
          clearError={clearError}
          reload={reload}
          report={report}
        />
      ) : tab === "client" ? (
        <ClientTab serverUrl={props.serverUrl} token={props.token} osName={osName} />
      ) : tab === "rcon" ? (
        <RconTab serverUrl={props.serverUrl} token={props.token} onError={report} />
      ) : tab === "logs" ? (
        <LogsTab
          logEvent={logEvent}
          setLogEvent={setLogEvent}
          logSince={logSince}
          onLogSince={onLogSince}
          logAuto={logAuto}
          setLogAuto={setLogAuto}
          loadLogs={(since) => void loadLogs(since)}
          logSaving={logSaving}
          onDownloadLog={() => void onDownloadLog()}
          logBox={logBox}
          shownLogs={shownLogs}
        />
      ) : tab === "changelog" ? (
        <ChangelogTab serverUrl={props.serverUrl} token={props.token} onSaved={clearError} onError={report} />
      ) : tab === "datapacks" ? (
        <DatapacksTab serverUrl={props.serverUrl} token={props.token} onSaved={clearError} onError={report} />
      ) : tab === "debug" ? (
        <DebugTab />
      ) : (
        <BackupsTab
          serverVersion={serverVersion}
          currentBackup={currentBackup}
          backupBytes={backupBytes}
          freeBytes={freeBytes}
          backupBusy={backupBusy}
          dailyTime={dailyTime}
          setDailyTime={setDailyTime}
          onSaveDaily={() => void onSaveDaily()}
          savedDaily={savedDaily}
          backupScope={backupScope}
          onBackupScope={(scope) => void onBackupScope(scope)}
          backupDest={backupDest}
          onBackupDest={(dest) => void onBackupDest(dest)}
          s3Endpoint={s3Endpoint}
          setS3Endpoint={setS3Endpoint}
          s3Region={s3Region}
          setS3Region={setS3Region}
          s3Bucket={s3Bucket}
          setS3Bucket={setS3Bucket}
          s3Prefix={s3Prefix}
          setS3Prefix={setS3Prefix}
          s3Access={s3Access}
          setS3Access={setS3Access}
          s3Secret={s3Secret}
          setS3Secret={setS3Secret}
          s3SecretSet={s3SecretSet}
          onSaveS3={() => void onSaveS3()}
          onCheckS3={() => void onCheckS3()}
          s3Note={s3Note}
          onMilestone={() => void onMilestone()}
          onBackup={() => void onBackup()}
          onUploadWorld={() => void onUploadWorld()}
          backupJob={backupJob}
          backups={backups}
          onRollback={(id) => void onRollback(id)}
          onLaunch={(id) => void onLaunch(id)}
          onDownload={(item) => void onDownload(item)}
          onDeleteBackup={(id) => void onDeleteBackup(id)}
        />
      )}
    </section>
  );
}
