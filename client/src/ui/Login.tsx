import { useState, type FormEvent } from "react";
import { ApiError, displayError, login } from "../api";
import { saveSession } from "../commands";
import type { AdminUser } from "../types";

interface Props {
  serverUrl: string;
  onSuccess: (token: string, admin: AdminUser) => void;
  onClose: () => void;
}

export function Login(props: Props) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError("");
    try {
      const result = await login(props.serverUrl.trim(), username, password);
      await saveSession(result.token);
      props.onSuccess(result.token, result.admin);
    } catch (err) {
      if (err instanceof ApiError && err.code === "unauthorized") {
        setError("Неверное имя или пароль.");
      } else {
        setError(displayError(err));
      }
    }
  }

  return (
    <div className="login-screen">
      <form className="card login-card" onSubmit={(event) => void submit(event)}>
        <div className="login-head">
          <h1>Вход</h1>
          <button type="button" className="btn-text" onClick={props.onClose}>
            Закрыть
          </button>
        </div>
        <div className="field">
          <label className="field-label" htmlFor="login-name">
            Имя
          </label>
          <input
            id="login-name"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
            autoComplete="username"
          />
        </div>
        <div className="field">
          <label className="field-label" htmlFor="login-password">
            Пароль
          </label>
          <input
            id="login-password"
            type="password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            autoComplete="current-password"
          />
        </div>
        <button type="submit" className="btn btn-primary btn-block">
          Войти
        </button>
        {error ? <p className="error">{error}</p> : null}
      </form>
    </div>
  );
}
