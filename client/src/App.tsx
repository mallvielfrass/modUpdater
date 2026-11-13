import { getVersion } from "@tauri-apps/api/app";
import { listen } from "@tauri-apps/api/event";
import { useEffect, useState } from "react";
import { ApiError, SERVER_URL, displayError, fetchClientLatest, fetchPublicMods, logout, me, setUnauthorizedHandler } from "./api";
import { appChannel, backupMod, currentOs, loadConfig, loadConfigRegistry, loadSession, saveConfig, saveSession, scanMods, selfUpdate } from "./commands";
import { isNewer, pickAsset } from "./semver";
import { syncPlan, type SyncItem } from "./sync";
import type { AdminUser } from "./types";
import { Admin } from "./ui/Admin";
import { Backup } from "./ui/Backup";
import { Login } from "./ui/Login";
import { Settings } from "./ui/Settings";
import { UserMods } from "./ui/UserMods";

type View = "user" | "login" | "admin";
type PlayerTab = "mods" | "backup";

export default function App() {
  const [view, setView] = useState<View>("user");
  const [playerTab, setPlayerTab] = useState<PlayerTab>("mods");
  const [modsDir, setModsDir] = useState("");
  const [token, setToken] = useState("");
  const [selfAdmin, setSelfAdmin] = useState<AdminUser | null>(null);
  const [items, setItems] = useState<SyncItem[] | null>(null);
  const [registry, setRegistry] = useState<Record<string, string>>({});
  const [settingsError, setSettingsError] = useState("");
  const [busy, setBusy] = useState(false);
  const [modsBusy, setModsBusy] = useState(false);
  const [version, setVersion] = useState("");
  const [dev, setDev] = useState(false);
  const [updateLabel, setUpdateLabel] = useState("");
  const [updatePercent, setUpdatePercent] = useState(0);

  useEffect(() => {
    setUnauthorizedHandler(async () => {
      try {
        await saveSession("");
      } finally {
        setToken("");
        setSelfAdmin(null);
        setView("login");
      }
    });
    let cancelled = false;
    void (async () => {
      let versionText = "";
      let channel = "";
      try {
        [versionText, channel] = await Promise.all([getVersion().catch(() => ""), appChannel().catch(() => "")]);
      } catch {
        versionText = "";
        channel = "";
      }
      if (cancelled) return;
      setVersion(versionText);
      setDev(channel === "dev");
      if (channel !== "dev" && versionText) {
        try {
          const latest = await fetchClientLatest(SERVER_URL);
          const os = await currentOs();
          const asset = pickAsset(os, latest.linux_sha256, latest.linux_size, latest.windows_sha256, latest.windows_size);
          if (latest.version && asset && isNewer(latest.version, versionText)) {
            if (!cancelled) setUpdateLabel(`Обновление клиента до v${latest.version}`);
            const unlisten = await listen<{ loaded: number; total: number }>("update-progress", (event) => {
              const total = event.payload.total;
              const pct = total > 0 ? Math.round((event.payload.loaded / total) * 100) : 0;
              setUpdatePercent(Math.min(100, pct));
            });
            try {
              await selfUpdate(latest.version, asset.sha256);
            } finally {
              unlisten();
            }
          }
        } catch (err) {
          if (!cancelled) setSettingsError(displayError(err));
        }
      }
      if (cancelled) return;
      let dir = "";
      try {
        const cfg = await loadConfig();
        dir = cfg.mods_dir.trim();
        setModsDir(cfg.mods_dir);
      } catch {
        setModsDir("");
      }
      try {
        setToken(await loadSession());
      } catch {
        setToken("");
      }
      if (!dir) return;
      setBusy(true);
      setSettingsError("");
      try {
        if (cancelled) return;
        await refreshPlan();
        if (cancelled) return;
        try {
          setRegistry(await loadConfigRegistry());
        } catch {
          setRegistry({});
        }
      } catch (err) {
        if (!cancelled) setSettingsError(displayError(err));
      } finally {
        if (!cancelled) setBusy(false);
      }
    })();
    return () => {
      cancelled = true;
      setUnauthorizedHandler(null);
    };
  }, []);

  async function onCheck(dir = modsDir) {
    const clean = dir.trim();
    if (!clean) return;
    setBusy(true);
    setSettingsError("");
    try {
      await saveConfig(clean);
      setModsDir(clean);
      await refreshPlan();
      try {
        setRegistry(await loadConfigRegistry());
      } catch {
        setRegistry({});
      }
    } catch (err) {
      setSettingsError(displayError(err));
    } finally {
      setBusy(false);
    }
  }

  async function refreshPlan() {
    const remote = await fetchPublicMods(SERVER_URL);
    let local = await scanMods();
    let plan = syncPlan(remote.mods, local, remote.revoked);
    const notes: string[] = [];
    let removed = false;
    for (const item of plan) {
      if (item.action !== "remove") continue;
      removed = true;
      try {
        await backupMod(item.filename);
      } catch (err) {
        notes.push(`${item.name}: ${displayError(err)}`);
      }
    }
    if (removed) {
      local = await scanMods();
      plan = syncPlan(remote.mods, local, remote.revoked);
    }
    setItems(plan);
    if (notes.length > 0) throw new Error(notes.join(". "));
  }

  async function reloadPlan() {
    const dir = modsDir.trim();
    if (!dir) return;
    await refreshPlan();
    try {
      setRegistry(await loadConfigRegistry());
    } catch {
      setRegistry({});
    }
  }

  async function onLock() {
    try {
      const cfg = await loadConfig();
      setModsDir(cfg.mods_dir);
    } catch {
      setModsDir("");
    }
    let current = "";
    try {
      current = await loadSession();
      setToken(current);
    } catch {
      current = "";
    }
    if (!current) {
      setView("login");
      return;
    }
    try {
      const result = await me(SERVER_URL, current);
      setSelfAdmin(result.admin);
      setToken(current);
      setView("admin");
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) return;
      setSettingsError(displayError(err));
    }
  }

  async function onLogout() {
    try {
      if (token) await logout(SERVER_URL, token);
    } catch {
      // Local session is cleared either way.
    }
    await saveSession("");
    setToken("");
    setSelfAdmin(null);
    setView("user");
  }

  async function onSelfDeleted() {
    await saveSession("");
    setToken("");
    setSelfAdmin(null);
    setView("user");
  }

  return (
    <div className="app">
      {updateLabel ? (
        <div className="meter">
          <span className="progress">{updateLabel}</span>
          <div className="meter-track" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={updatePercent}>
            <div className="meter-fill" style={{ width: `${updatePercent}%` }} />
          </div>
        </div>
      ) : null}
      {view === "admin" && selfAdmin ? (
        <Admin
          serverUrl={SERVER_URL}
          token={token}
          selfId={selfAdmin.id}
          version={version}
          dev={dev}
          onClose={() => setView("user")}
          onLogout={() => void onLogout()}
          onSelfDeleted={() => void onSelfDeleted()}
        />
      ) : view === "login" ? (
        <Login
          serverUrl={SERVER_URL}
          onSuccess={(next, admin) => {
            setToken(next);
            setSelfAdmin(admin);
            setView("admin");
          }}
          onClose={() => setView("user")}
        />
      ) : (
        <>
          <header className="appbar">
            <div className="brand">
              modupdater
              {version ? <span className="ver">v{version}</span> : null}
              {dev ? <span className="badge">dev</span> : null}
            </div>
            <button type="button" className="icon-btn" disabled={modsBusy} onClick={() => void onLock()} aria-label="Вход">
              <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true">
                <rect x="5" y="11" width="14" height="10" rx="1" fill="none" stroke="currentColor" strokeWidth="2" />
                <path d="M8 11V8a4 4 0 0 1 8 0v3" fill="none" stroke="currentColor" strokeWidth="2" />
              </svg>
            </button>
          </header>
          <Settings
            modsDir={modsDir}
            error={settingsError}
            busy={busy || modsBusy || updateLabel !== ""}
            onModsDir={setModsDir}
            onPick={(dir) => void onCheck(dir)}
            onCheck={() => void onCheck()}
          />
          <div className="tabs" role="tablist">
            <button type="button" role="tab" aria-selected={playerTab === "mods"} onClick={() => setPlayerTab("mods")}>
              Моды
            </button>
            <button type="button" role="tab" aria-selected={playerTab === "backup"} onClick={() => setPlayerTab("backup")}>
              Бэкап
            </button>
          </div>
          {playerTab === "backup" ? (
            <Backup modsDir={modsDir} />
          ) : (
            <UserMods items={items} registry={registry} onChanged={reloadPlan} onBusy={setModsBusy} />
          )}
        </>
      )}
    </div>
  );
}
