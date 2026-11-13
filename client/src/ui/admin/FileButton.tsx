export function FileButton(props: { file: File | null; disabled?: boolean; onFile: (file: File | null) => void }) {
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
