import { useEffect, useRef } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { setPropFilter } from '../state/filters';

// Phase 5 of segments — inline + Property add affordance. Renders a
// + Property button; click opens a 3-step popover (scope → name →
// value). Esc closes, Enter submits, Tab advances. Keyboard map
// validated against Linear / Raycast / WAI-ARIA APG per plan § 11.3.

type Scope = 'hitProps' | 'sessionProps' | 'userProps';

const SCOPES: Array<{ id: Scope; label: string }> = [
  { id: 'hitProps', label: 'HIT' },
  { id: 'sessionProps', label: 'SES' },
  { id: 'userProps', label: 'USR' },
];

export function PropFilterAdd() {
  const open = useSignal(false);
  const scope = useSignal<Scope>('hitProps');
  const name = useSignal('');
  const value = useSignal('');
  const nameRef = useRef<HTMLInputElement>(null);

  // Autofocus the name input when the popover opens — keyboard flow
  // continues with Tab / Enter from there.
  useEffect(() => {
    if (open.value && nameRef.current) {
      nameRef.current.focus();
    }
  }, [open.value]);

  // Esc closes the popover from any field. Listener attached to
  // window so it works regardless of which child has focus.
  useEffect(() => {
    if (!open.value) return;

    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        open.value = false;
      }
    };

    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open.value]);

  // `/` from anywhere on the dashboard opens the popover and focuses
  // name. Mirrors the Linear / Stripe / Datadog convention validated
  // in research § 6 + plan § 11.3.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === '/' && !open.value) {
        const target = e.target as HTMLElement | null;
        if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) {
          return;
        }
        e.preventDefault();
        open.value = true;
      }
    };

    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const canSubmit = name.value.trim() !== '' && value.value.trim() !== '';

  const submit = () => {
    if (!canSubmit) return;
    setPropFilter(scope.value, name.value.trim(), value.value.trim());
    name.value = '';
    value.value = '';
    open.value = false;
  };

  return (
    <span class="seg-add-wrap">
      <button
        type="button"
        class="seg-add-btn"
        onClick={() => { open.value = !open.value; }}
        aria-haspopup="dialog"
        aria-expanded={open.value}
        title="Add a property filter (press /)"
      >
        + Property
      </button>

      {open.value && (
        <div class="seg-add-popover" role="dialog" aria-modal="false" aria-label="Add property filter">
          <div class="seg-add-scope" role="radiogroup" aria-label="Scope">
            {SCOPES.map((s) => (
              <button
                key={s.id}
                type="button"
                role="radio"
                aria-pressed={scope.value === s.id}
                aria-checked={scope.value === s.id}
                onClick={() => { scope.value = s.id; }}
              >
                {s.label}
              </button>
            ))}
          </div>

          <input
            ref={nameRef}
            type="text"
            placeholder="Property name"
            value={name.value}
            onInput={(e) => { name.value = (e.target as HTMLInputElement).value; }}
            aria-label="Property name"
          />

          <input
            type="text"
            placeholder="Value"
            value={value.value}
            onInput={(e) => { value.value = (e.target as HTMLInputElement).value; }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                submit();
              }
            }}
            aria-label="Property value"
          />

          <button
            type="button"
            class="seg-add-submit"
            onClick={submit}
            disabled={!canSubmit}
          >
            Add filter
          </button>
        </div>
      )}
    </span>
  );
}
