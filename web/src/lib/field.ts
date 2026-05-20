// Validator<T> returns null when the value is acceptable, the error
// sentence otherwise. Pure — no I/O — so the Save button can run the
// full form validator on every keystroke without throttling.
export type Validator<T> = (value: T) => string | null;

// FieldHint bundles the three artifacts every admin-panel field ships
// with: a plain-language hint shown under the control, the validator
// that runs on every change, and a type-erased errorFor() the JSX
// layer calls without re-asserting the value shape. `validator` is
// widened to Validator<unknown> so a FieldHint can sit in a
// heterogenous registry; the validators below all accept `unknown`.
export interface FieldHint {
  id: string;
  label: string;
  hint: string;
  validator: Validator<unknown>;
  errorFor(value: unknown): string | null;
}

const HOSTNAME_RE = /^[a-z0-9.-]+(?::\d+)?$/;
const SLUG_RE = /^[a-z0-9-]+$/;
const EMAIL_RE = /^\S+@\S+\.\S+$/;
const USERNAME_RE = /^[a-z0-9._-]{2,32}$/;
const HTTPS_ORIGIN_RE = /^https:\/\/[a-z0-9.-]+(?::\d+)?$/i;
const EVENT_NAME_RE = /^[a-z0-9_]{1,128}$/;

const isString = (v: unknown): v is string => typeof v === 'string';

// oneOf builds a membership validator against a closed list. Used for
// dropdown-bound fields like jurisdiction, consent_mode, and role —
// the server is the authority, the client copy is the friendly
// pre-empt so the user fixes the dropdown rather than chasing a 422.
export function oneOf<T>(allowed: readonly T[]): Validator<T> {
  return (value: T): string | null =>
    allowed.includes(value) ? null : 'Pick a value from the list.';
}

// validators is the registry every panel field references. Adding a
// new field shape goes here so the per-field error sentence lives in
// exactly one place; the JSX layer never inlines a regex.
export const validators = {
  hostname: (value: unknown): string | null => {
    if (!isString(value) || value.length === 0 || value.length > 253 || !HOSTNAME_RE.test(value)) {
      return 'Hostname must be a domain like `example.com` (no `https://`, no `/`).';
    }
    return null;
  },
  slug: (value: unknown): string | null => {
    if (value === undefined || value === null || value === '') return null;
    if (!isString(value) || value.length < 2 || value.length > 32 || !SLUG_RE.test(value)) {
      return 'Slug can use letters, numbers, and hyphens only. Up to 32 characters.';
    }
    return null;
  },
  email: (value: unknown): string | null =>
    isString(value) && EMAIL_RE.test(value) ? null : "That doesn't look like an email address.",
  username: (value: unknown): string | null =>
    isString(value) && USERNAME_RE.test(value)
      ? null
      : 'Username can use letters, numbers, dots, hyphens, and underscores. Up to 32 characters.',
  password: (value: unknown): string | null =>
    isString(value) && value.length >= 12 ? null : 'Password needs at least 12 characters.',
  httpsOrigin: (value: unknown): string | null => {
    if (isString(value) && HTTPS_ORIGIN_RE.test(value)) return null;
    const shown = isString(value) ? value : String(value ?? '');
    return `Must start with \`https://\` and have no \`/\` or path. You entered \`${shown}\`.`;
  },
  eventName: (value: unknown): string | null =>
    isString(value) && EVENT_NAME_RE.test(value)
      ? null
      : 'Event name uses lowercase letters, numbers, and underscores only. Up to 128 characters.',
  goalValue: (value: unknown): string | null => {
    const n = typeof value === 'number' ? value : Number(value);
    if (!Number.isFinite(n) || !Number.isInteger(n) || n < 0 || String(n).length > 10) {
      return 'Value must be 0 or a positive whole number.';
    }
    return null;
  },
  oneOf,
} as const;
