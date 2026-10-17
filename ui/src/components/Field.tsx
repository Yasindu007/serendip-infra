import type { InputHTMLAttributes } from "react";

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
