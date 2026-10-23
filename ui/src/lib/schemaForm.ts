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

/**
 * deref follows a local reference ("#/$defs/Name" or "#/definitions/Name")
 * into root, as platform schemas share definitions that way. Anything
 * else, or a reference that doesn't resolve, is left as it is (and so
 * edited as JSON).
 */
function deref(s: JSONSchema, root: JSONSchema, depth = 0): JSONSchema {
  const ref = s.$ref;
  if (typeof ref !== "string" || depth > 8) return s;
  const m = /^#\/(\$defs|definitions)\/([^/]+)$/.exec(ref);
  const target = m ? (root[m[1] as "$defs" | "definitions"] as Record<string, JSONSchema> | undefined)?.[m[2]!] : undefined;
  return target ? deref({ ...target, ...withoutRef(s) }, root, depth + 1) : s;
}

function withoutRef(s: JSONSchema): JSONSchema {
  const { $ref: _ref, ...rest } = s;
  return rest;
}

/** Keys never used as a path step: they'd reach Object.prototype. */
const unsafeKeys = new Set(["__proto__", "constructor", "prototype"]);

/** How deep nested objects become fields; a schema that refers to itself stops here. */
const MAX_DEPTH = 6;

/** The fields a schema describes, in its own property order. */
export function schemaFields(schema: JSONSchema | undefined, prefix = "", group = "", root: JSONSchema | undefined = schema, depth = 0): SchemaField[] {
  if (!schema || !root || depth > MAX_DEPTH) return [];
  const own = deref(schema, root);
  if (!isObject(own)) return [];
  const required = new Set(own.required ?? []);
  return Object.entries(own.properties ?? {}).flatMap(([name, raw]) => {
    if (unsafeKeys.has(name)) return [];
    const s = deref(raw, root);
    const path = prefix ? `${prefix}.${name}` : name;
    if (isObject(s)) return schemaFields(s, path, path, root, depth + 1);
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
