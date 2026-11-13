export function ErrorNote(props: { message: string; log: string }) {
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
