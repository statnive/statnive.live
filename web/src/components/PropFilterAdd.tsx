import { useEffect, useMemo, useRef } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { apiGet } from '../api/client';
import { siteSignal } from '../state/site';
import { setPropFilter } from '../state/filters';

// Phase 5 of segments — inline + Property add affordance with key+value
// autocomplete. Click (or `/`) opens a popover (scope → name → value).
// Both name and value fields search /api/props/list?scope=X live; novel
// values are accepted on Enter so operators aren't gated by the cache.
// Esc closes, Enter submits, Tab advances. Keyboard map validated
// against Linear / Raycast / WAI-ARIA APG per plan § 11.3.

type Scope = 'hitProps' | 'sessionProps' | 'userProps';

const SCOPES: Array<{ id: Scope; label: string; apiScope: 'hit' | 'session' | 'user'; help: string }> = [
  { id: 'hitProps', label: 'HIT', apiScope: 'hit', help: 'Applies to this single event.' },
  { id: 'sessionProps', label: 'SES', apiScope: 'session', help: 'Applies to every event in this session.' },
  { id: 'userProps', label: 'USR', apiScope: 'user', help: 'Applies to every event from this visitor.' },
];

interface PropNameRow {
  name: string;
  sample_values: string[];
  last_seen: string;
}

// Module-level cache keyed by (site_id, scope). The cache survives popover
// open/close so the second open is instant. Invalidated only by SiteSwitcher
// (siteSignal change triggers a re-fetch in the effect below).
const propsCache = new Map<string, PropNameRow[]>();

function cacheKey(siteID: number, scope: string): string {
  return `${siteID}:${scope}`;
}

export function PropFilterAdd() {
  const open = useSignal(false);
  const scope = useSignal<Scope>('hitProps');
  const name = useSignal('');
  const value = useSignal('');
  const props = useSignal<PropNameRow[]>([]);
  const focused = useSignal<'name' | 'value' | null>(null);
  const highlight = useSignal(0);
  const nameRef = useRef<HTMLInputElement>(null);
  const valueRef = useRef<HTMLInputElement>(null);

  const scopeDef = SCOPES.find((s) => s.id === scope.value)!;
  const apiScope = scopeDef.apiScope;

  // Fetch the prop catalog for the active scope when the popover opens
  // or the scope toggles. Cached per (site, scope) so re-opening is free.
  useEffect(() => {
    if (!open.value) return;

    const siteID = siteSignal.value;
    const key = cacheKey(siteID, apiScope);
    const cached = propsCache.get(key);

    if (cached) {
      props.value = cached;
      return;
    }

    const ctrl = new AbortController();
    apiGet<PropNameRow[]>('/api/props/list', { scope: apiScope }, ctrl.signal)
      .then((rows) => {
        propsCache.set(key, rows);
        if (!ctrl.signal.aborted) props.value = rows;
      })
      .catch(() => {
        if (!ctrl.signal.aborted) props.value = [];
      });

    return () => ctrl.abort();
  }, [open.value, apiScope]);

  // Autofocus name when the popover opens.
  useEffect(() => {
    if (open.value && nameRef.current) {
      nameRef.current.focus();
      focused.value = 'name';
    }
  }, [open.value]);

  // Esc closes from any field; listener on window so it works regardless
  // of which child has focus.
  useEffect(() => {
    if (!open.value) return;

    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') open.value = false;
    };

    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open.value]);

  // `/` from anywhere on the dashboard opens the popover (Linear / Stripe
  // / Datadog convention; plan § 11.3).
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

  // Filtered name suggestions — substring, case-insensitive, top 6 per
  // plan § 11.3. Empty query returns the full catalog (capped at 6).
  const nameSuggestions = useMemo(() => {
    const q = name.value.trim().toLowerCase();
    const rows = q === ''
      ? props.value
      : props.value.filter((r) => r.name.toLowerCase().includes(q));
    return rows.slice(0, 6);
  }, [name.value, props.value]);

  // Filtered value suggestions. Prefer the sample_values for the row
  // matching the typed name; if no match, surface the union of all
  // sample_values in the scope (deduped, sorted), filtered by query.
  // Same 6-row cap.
  const valueSuggestions = useMemo(() => {
    const q = value.value.trim().toLowerCase();
    const typed = name.value.trim();
    const exact = props.value.find((r) => r.name === typed);
    const pool = exact
      ? exact.sample_values
      : Array.from(new Set(props.value.flatMap((r) => r.sample_values))).sort();
    const filtered = q === '' ? pool : pool.filter((v) => v.toLowerCase().includes(q));
    return filtered.slice(0, 6);
  }, [value.value, name.value, props.value]);

  const activeSuggestions = focused.value === 'value' ? valueSuggestions : nameSuggestions;

  // Reset highlight whenever the suggestion list changes or focus moves
  // between the two fields, so ↑/↓ start from the top.
  useEffect(() => {
    highlight.value = 0;
  }, [focused.value, name.value, value.value]);

  const canSubmit = name.value.trim() !== '' && value.value.trim() !== '';

  const submit = () => {
    if (!canSubmit) return;
    setPropFilter(scope.value, name.value.trim(), value.value.trim());
    name.value = '';
    value.value = '';
    open.value = false;
  };

  const onListKey = (e: KeyboardEvent, field: 'name' | 'value') => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      highlight.value = Math.min(highlight.value + 1, Math.max(activeSuggestions.length - 1, 0));
      return;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      highlight.value = Math.max(highlight.value - 1, 0);
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      const pick = activeSuggestions[highlight.value];
      if (field === 'name') {
        if (pick) {
          name.value = typeof pick === 'string' ? pick : pick.name;
          valueRef.current?.focus();
        } else if (name.value.trim() !== '') {
          valueRef.current?.focus();
        }
      } else {
        if (pick) value.value = typeof pick === 'string' ? pick : pick.name;
        submit();
      }
    }
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
                title={s.help}
                onClick={() => { scope.value = s.id; }}
              >
                {s.label}
              </button>
            ))}
          </div>
          <p class="seg-add-scope-help">{scopeDef.help}</p>

          <div class="seg-add-field">
            <input
              ref={nameRef}
              type="text"
              placeholder="Property name"
              value={name.value}
              role="combobox"
              aria-autocomplete="list"
              aria-expanded={focused.value === 'name' && nameSuggestions.length > 0}
              aria-controls="seg-add-name-list"
              aria-activedescendant={focused.value === 'name' && nameSuggestions.length > 0 ? `seg-add-name-opt-${highlight.value}` : undefined}
              onFocus={() => { focused.value = 'name'; }}
              onInput={(e) => { name.value = (e.target as HTMLInputElement).value; }}
              onKeyDown={(e) => onListKey(e, 'name')}
              aria-label="Property name"
            />
            {focused.value === 'name' && nameSuggestions.length > 0 && (
              <ul id="seg-add-name-list" role="listbox" class="seg-add-list">
                {nameSuggestions.map((row, i) => (
                  <li
                    key={row.name}
                    id={`seg-add-name-opt-${i}`}
                    role="option"
                    aria-selected={i === highlight.value}
                    class={'seg-add-list-row' + (i === highlight.value ? ' is-active' : '')}
                    onMouseDown={(e) => {
                      e.preventDefault();
                      name.value = row.name;
                      valueRef.current?.focus();
                    }}
                  >
                    <span class="seg-add-list-name">{row.name}</span>
                    <span class="seg-add-list-count">{row.sample_values.length}v</span>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div class="seg-add-field">
            <input
              ref={valueRef}
              type="text"
              placeholder="Value"
              value={value.value}
              role="combobox"
              aria-autocomplete="list"
              aria-expanded={focused.value === 'value' && valueSuggestions.length > 0}
              aria-controls="seg-add-value-list"
              aria-activedescendant={focused.value === 'value' && valueSuggestions.length > 0 ? `seg-add-value-opt-${highlight.value}` : undefined}
              onFocus={() => { focused.value = 'value'; }}
              onInput={(e) => { value.value = (e.target as HTMLInputElement).value; }}
              onKeyDown={(e) => onListKey(e, 'value')}
              aria-label="Property value"
            />
            {focused.value === 'value' && valueSuggestions.length > 0 && (
              <ul id="seg-add-value-list" role="listbox" class="seg-add-list">
                {valueSuggestions.map((v, i) => (
                  <li
                    key={v}
                    id={`seg-add-value-opt-${i}`}
                    role="option"
                    aria-selected={i === highlight.value}
                    class={'seg-add-list-row' + (i === highlight.value ? ' is-active' : '')}
                    onMouseDown={(e) => {
                      e.preventDefault();
                      value.value = v;
                      submit();
                    }}
                  >
                    <span class="seg-add-list-name">{v}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <button
            type="button"
            class="seg-add-submit"
            onClick={submit}
            disabled={!canSubmit}
          >
            Apply filter
          </button>
          <p class="seg-add-kbd-hint">Press <kbd>/</kbd> from anywhere to open this.</p>
        </div>
      )}
    </span>
  );
}
