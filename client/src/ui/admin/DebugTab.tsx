import { useSyncExternalStore } from "react";
import { adminDebugSnapshot, subscribeAdminDebug } from "../debugLog";

export function DebugTab() {
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
