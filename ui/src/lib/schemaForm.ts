// Forms generated from the platform's JSON Schemas (spec 020): a
// Component's per-environment configuration and its traits' settings.
// Only the common shapes become fields (strings, numbers, booleans, enums,
// nested objects); anything else is edited as JSON. The server checks every
// value against the same schema and refuses what doesn't fit, so this is
// a convenience, never the gate.
import type { JSONSchema } from "../api/types";

export type FieldKind = "text" | "integer" | "number" | "boolean" | "enum" | "json";

export interface SchemaField {
  /** Dotted path from the root, such as "resources.limits.cpu". */
  path: string;
  label: string;
  kind: FieldKind;
  schema: JSONSchema;
  required: boolean;
  /** The heading path of the nested object it belongs to, "" at the root. */
  group: string;
}

/** Form state: every field's text, by path. Booleans are "true" or "false"; "" means not set. */
export type SchemaFormState = Record<string, string>;

const typeOf = (s: JSONSchema): string | undefined => (Array.isArray(s.type) ? s.type.find((t) => t !== "null") : s.type);

function kindOf(s: JSONSchema): FieldKind {
  if (Array.isArray(s.enum) && s.enum.length > 0 && s.enum.every((v) => typeof v === "string")) return "enum";
  switch (typeOf(s)) {
    case "string":
      return "text";
    case "integer":
      return "integer";
    case "number":
      return "number";
    case "boolean":
      return "boolean";
    default:
      return "json";
  }
}

const isObject = (s: JSONSchema) => typeOf(s) === "object" && s.properties !== undefined;

/** The fields a schema describes, in its own property order. */
export function schemaFields(schema: JSONSchema | undefined, prefix = "", group = ""): SchemaField[] {
  if (!schema || !isObject(schema)) return [];
  const required = new Set(schema.required ?? []);
  return Object.entries(schema.properties ?? {}).flatMap(([name, s]) => {
    const path = prefix ? `${prefix}.${name}` : name;
    if (isObject(s)) return schemaFields(s, path, path);
    return [{ path, label: s.title ?? name, kind: kindOf(s), schema: s, required: required.has(name), group }];
  });
}

function get(obj: unknown, path: string): unknown {
  return path.split(".").reduce<unknown>((o, k) => (o !== null && typeof o === "object" ? (o as Record<string, unknown>)[k] : undefined), obj);
}

function set(obj: Record<string, unknown>, path: string, value: unknown) {
  const keys = path.split(".");
  let o = obj;
  for (const k of keys.slice(0, -1)) {
    if (o[k] === null || typeof o[k] !== "object") o[k] = {};
    o = o[k] as Record<string, unknown>;
  }
  o[keys[keys.length - 1]!] = value;
}

/** The form state for stored values. */
export function toFormState(schema: JSONSchema | undefined, values: Record<string, unknown> | undefined): SchemaFormState {
  const state: SchemaFormState = {};
  for (const f of schemaFields(schema)) {
    const v = get(values, f.path);
    state[f.path] = v === undefined ? "" : f.kind === "json" ? JSON.stringify(v) : String(v);
  }
  return state;
}

/**
 * The values to send, and an error for each field that can't become one.
 * An empty field is left out, so the platform's default applies.
 */
export function fromFormState(schema: JSONSchema | undefined, state: SchemaFormState): { values: Record<string, unknown>; errors: Record<string, string> } {
  const values: Record<string, unknown> = {};
  const errors: Record<string, string> = {};
  for (const f of schemaFields(schema)) {
    const text = (state[f.path] ?? "").trim();
    if (text === "") {
      if (f.required) errors[f.path] = `Enter ${f.label}.`;
      continue;
    }
    const s = f.schema;
    let v: unknown = text;
    switch (f.kind) {
      case "integer":
      case "number": {
        const n = Number(text);
        if (!Number.isFinite(n) || (f.kind === "integer" && !Number.isInteger(n))) {
          errors[f.path] = f.kind === "integer" ? "Enter a whole number." : "Enter a number.";
          continue;
        }
        if (s.minimum !== undefined && n < s.minimum) errors[f.path] = `At least ${s.minimum}.`;
        else if (s.maximum !== undefined && n > s.maximum) errors[f.path] = `At most ${s.maximum}.`;
        v = n;
        break;
      }
      case "boolean":
        v = text === "true";
        break;
      case "json":
        try {
          v = JSON.parse(text);
        } catch {
          errors[f.path] = "Enter valid JSON.";
          continue;
        }
        break;
      default:
        if (s.minLength !== undefined && text.length < s.minLength) errors[f.path] = `At least ${s.minLength} characters.`;
        else if (s.maxLength !== undefined && text.length > s.maxLength) errors[f.path] = `At most ${s.maxLength} characters.`;
    }
    set(values, f.path, v);
  }
  return { values, errors };
}
