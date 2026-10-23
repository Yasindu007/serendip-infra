import type { JSONSchema } from "../api/types";
import { schemaFields, type SchemaField, type SchemaFormState } from "../lib/schemaForm";
import { SelectField, TextAreaField, TextField } from "./Field";

const notSet = { value: "", label: "Not set (the platform's default)" };

function hintOf(f: SchemaField): string | undefined {
  const parts = [f.schema.description, f.schema.default !== undefined ? `Default ${JSON.stringify(f.schema.default)}.` : undefined, f.kind === "json" ? "JSON." : undefined];
  const text = parts.filter(Boolean).join(" ");
  return text === "" ? undefined : text;
}

function Field({ f, id, value, error, onChange }: { f: SchemaField; id: string; value: string; error: string | undefined; onChange: (v: string) => void }) {
  const hint = hintOf(f);
  const common = { id, label: f.required ? f.label : `${f.label} (optional)`, ...(hint ? { hint } : {}), ...(error ? { error } : {}) };
  switch (f.kind) {
    case "enum":
      return <SelectField {...common} value={value} onChange={(e) => onChange(e.target.value)} options={[notSet, ...(f.schema.enum as string[]).map((v) => ({ value: v, label: v }))]} />;
    case "boolean":
      return (
        <SelectField
          {...common}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          options={[notSet, { value: "true", label: "Yes" }, { value: "false", label: "No" }]}
        />
      );
    case "json":
      return <TextAreaField {...common} value={value} onChange={(e) => onChange(e.target.value)} />;
    default:
      return <TextField {...common} mono inputMode={f.kind === "text" ? undefined : "decimal"} value={value} onChange={(e) => onChange(e.target.value)} />;
  }
}

/**
 * SchemaForm renders the fields a platform JSON Schema describes (spec
 * 020), grouped by nested object. The schema comes from the API; the
 * server checks the values against it again.
 */
export function SchemaForm({
  idPrefix,
  schema,
  state,
  errors,
  onChange,
}: {
  idPrefix: string;
  schema: JSONSchema | undefined;
  state: SchemaFormState;
  errors: Record<string, string>;
  onChange: (path: string, value: string) => void;
}) {
  const fields = schemaFields(schema);
  if (fields.length === 0) return <p className="field-hint">The platform publishes no settings here.</p>;
  const groups = [...new Set(fields.map((f) => f.group))];
  const render = (f: SchemaField) => (
    <Field key={f.path} f={f} id={`${idPrefix}-${f.path.replace(/\./g, "-")}`} value={state[f.path] ?? ""} error={errors[f.path]} onChange={(v) => onChange(f.path, v)} />
  );
  return (
    <div className="rows">
      {groups.map((g) =>
        g === "" ? (
          fields.filter((f) => f.group === "").map(render)
        ) : (
          <fieldset key={g}>
            <legend className="mono">{g}</legend>
            <div className="rows">{fields.filter((f) => f.group === g).map(render)}</div>
          </fieldset>
        ),
      )}
    </div>
  );
}
