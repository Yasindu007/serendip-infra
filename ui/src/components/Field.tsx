import type { InputHTMLAttributes, SelectHTMLAttributes } from "react";

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
  options,
  ...select
}: SelectProps & { id: string; label: string; hint?: string; options: Array<{ value: string; label: string }> }) {
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <select id={id} className="input" aria-describedby={hint ? `${id}-hint` : undefined} {...select}>
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
    </div>
  );
}
