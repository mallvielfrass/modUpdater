import type { AdminUser } from "../../types";

export function AdminsTab(props: {
  inviteUrl: string;
  admins: AdminUser[];
  onInvite: () => void;
  onDeleteAdmin: (id: string) => void;
}) {
  const { inviteUrl, admins, onInvite, onDeleteAdmin } = props;
  return (
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
  );
}
