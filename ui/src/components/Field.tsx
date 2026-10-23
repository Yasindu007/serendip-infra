import type { InputHTMLAttributes, SelectHTMLAttributes, TextareaHTMLAttributes } from "react";

type InputProps = Omit<InputHTMLAttributes<HTMLInputElement>, "id" | "className">;

/**
 * TextField ties a label, an optional hint and an optional error to one
 * input, so screen readers announce all three.
 */
export function TextField({
  id,
  label,
  hint,
  error,
  mono = false,
  ...input
}: InputProps & { id: string; label: string; hint?: string; error?: string; mono?: boolean }) {
  const described = [hint ? `${id}-hint` : "", error ? `${id}-error` : ""].filter(Boolean).join(" ") || undefined;
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <input
        id={id}
        className={mono ? "input input--mono" : "input"}
        aria-invalid={error ? true : undefined}
        aria-describedby={described}
        autoComplete="off"
        spellCheck={false}
        {...input}
      />
      {hint && (
        <span id={`${id}-hint`} className="field-hint">
          {hint}
        </span>
      )}
      {error && (
        <span id={`${id}-error`} className="field-error">
          {error}
        </span>
      )}
    </div>
  );
}

type SelectProps = Omit<SelectHTMLAttributes<HTMLSelectElement>, "id" | "className">;

/** SelectField is TextField for a fixed set of choices. */
export function SelectField({
  id,
  label,
  hint,
  error,
  options,
  ...select
}: SelectProps & { id: string; label: string; hint?: string; error?: string; options: Array<{ value: string; label: string }> }) {
  const described = [hint ? `${id}-hint` : "", error ? `${id}-error` : ""].filter(Boolean).join(" ") || undefined;
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <select id={id} className="input" aria-invalid={error ? true : undefined} aria-describedby={described} {...select}>
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      {hint && (
        <span id={`${id}-hint`} className="field-hint">
          {hint}
        </span>
      )}
      {error && (
        <span id={`${id}-error`} className="field-error">
          {error}
        </span>
      )}
    </div>
  );
}

type TextAreaProps = Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, "id" | "className">;

/** TextAreaField is TextField for multi-line, monospace text such as JSON. */
export function TextAreaField({ id, label, hint, error, ...area }: TextAreaProps & { id: string; label: string; hint?: string; error?: string }) {
  const described = [hint ? `${id}-hint` : "", error ? `${id}-error` : ""].filter(Boolean).join(" ") || undefined;
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <textarea id={id} className="input input--mono input--area" aria-invalid={error ? true : undefined} aria-describedby={described} spellCheck={false} rows={3} {...area} />
      {hint && (
        <span id={`${id}-hint`} className="field-hint">
          {hint}
        </span>
      )}
      {error && (
        <span id={`${id}-error`} className="field-error">
          {error}
        </span>
      )}
    </div>
  );
}
