import { useEffect, useMemo, useState } from 'preact/hooks';
import {
  listGoals,
  createGoal,
  disableGoal,
  type AdminGoal,
} from '../../api/admin';
import { activeSiteSignal } from '../../state/site';
import { errorMessage } from '../../lib/errorMessage';
import { validators } from '../../lib/field';
import StatusPill from '../../components/StatusPill';
import CopyButton from '../../components/CopyButton';

export function GoalsTab() {
  const [rows, setRows] = useState<AdminGoal[] | null>(null);
  const [err, setErr] = useState<string>('');
  const activeSite = activeSiteSignal.value;
  const siteID = activeSite?.id ?? 0;

  async function refresh() {
    if (!siteID) return;
    try {
      setRows(await listGoals(siteID));
    } catch (e) {
      setErr(errorMessage(e, "Couldn't load goals."));
    }
  }

  useEffect(() => {
    void refresh();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [siteID]);

  async function onDisable(g: AdminGoal) {
    try {
      await disableGoal(g.site_id, g.goal_id);
      await refresh();
    } catch (e) {
      setErr(errorMessage(e, "Couldn't disable this goal. Try again."));
    }
  }

  return (
    <div class="statnive-admin-goals">
      <EventApiHelpCard />
      <NewGoalForm siteID={siteID} onCreated={refresh} onError={setErr} />

      {err ? (
        <p class="statnive-admin-alert is-error" role="alert">
          <span class="statnive-admin-alert-glyph" aria-hidden="true">{'▪'}</span>
          <span>
            <span class="statnive-admin-alert-label">Error</span>
            <span class="statnive-admin-alert-body">{err}</span>
          </span>
        </p>
      ) : null}

      {rows === null ? (
        <p>Loading…</p>
      ) : rows.length === 0 ? (
        <p>No goals yet for this site.</p>
      ) : (
        <table class="statnive-admin-table" data-testid="admin-goals-table">
          <thead>
            <tr>
              <th>Site</th>
              <th>Name</th>
              <th>Pattern (event_name)</th>
              <th>Value</th>
              <th>Status</th>
              <th>Snippet</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((g) => (
              <tr key={g.goal_id}>
                <td>{g.hostname || '·'} <code>({g.site_id})</code></td>
                <td>{g.name}</td>
                <td><code>{g.pattern}</code></td>
                <td><span class="statnive-num-cell">{g.value}</span></td>
                <td><StatusPill state={g.enabled ? 'active' : 'disabled'} /></td>
                <td><GoalSnippetButton goal={g} /></td>
                <td>
                  {g.enabled ? (
                    <button type="button" onClick={() => void onDisable(g)}>
                      Disable
                    </button>
                  ) : null}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function EventApiHelpCard() {
  return (
    <details class="statnive-admin-help-card">
      <summary><strong>How to fire custom events</strong></summary>
      <div>
        <p>Every visit fires a pageview automatically. For a custom event (click, form submit, video play):</p>
        <pre><code>{`window.statniveLive.track(name, props, value)`}</code></pre>
        <ul>
          <li><strong>name</strong> is required. Becomes <code>event_name</code>.</li>
          <li><strong>props</strong> is optional. Default <code>{'{}'}</code>.</li>
          <li><strong>value</strong> is optional. Defaults to <code>0</code>.</li>
        </ul>
        <p><strong>When is an event also a goal?</strong> Define a goal below. When <code>event_name</code> matches a goal pattern, the server sets <code>is_goal=1</code>. The revenue card sums goal events.</p>
        <p><strong>Goal value (fixed vs dynamic):</strong></p>
        <ul>
          <li><strong>Value &gt; 0</strong> (e.g. <code>1</code> for a signup, <code>50</code> for a lead) becomes a fixed-revenue goal. The server overrides whatever the tracker sent. Every goal hit counts the same.</li>
          <li><strong>Value = 0</strong> is dynamic passthrough. The tracker-supplied value flows through untouched. Use this for e-commerce: <code>{`window.statniveLive.track('purchase', {order_id: 'X-1234'}, 4999)`}</code> records $49.99 in revenue (send minor units / integer to keep cents).</li>
        </ul>
        <p><strong>Edge cases:</strong></p>
        <ul>
          <li>No matching goal: stored as a regular custom event (<code>is_goal=0</code>). Not in the revenue card.</li>
          <li>Disabled goal: behaves as if it does not exist. Re-enabling does NOT backfill past events.</li>
          <li>Tracker loaded on an unregistered hostname: server returns 204, event silently dropped.</li>
        </ul>
      </div>
    </details>
  );
}

function GoalSnippetButton({ goal }: { goal: AdminGoal }) {
  const [open, setOpen] = useState(false);

  const snippet = `// Direct call:\nwindow.statniveLive.track('${goal.pattern}', {\n  page: window.location.pathname,\n});\n\n// Delegated click listener (install once):\ndocument.addEventListener('click', function (e) {\n  var a = e.target.closest('[data-statnive-goal="${goal.pattern}"]');\n  if (!a || !window.statniveLive) return;\n  window.statniveLive.track('${goal.pattern}', {\n    page: window.location.pathname,\n    href: a.href || '',\n  });\n}, true);\n\n// Mark your element:\n// <a href="..." data-statnive-goal="${goal.pattern}">Click me</a>`;

  return (
    <span>
      <button type="button" class="statnive-chip" onClick={() => setOpen(!open)}>
        {open ? 'Hide' : 'Show snippet'}
      </button>
      {open ? (
        <span class="statnive-admin-snippet">
          <pre><code>{snippet}</code></pre>
          <CopyButton text={snippet} />
        </span>
      ) : null}
    </span>
  );
}

// goalNameError validates the free-text Name field inline because
// the lib/field.ts registry intentionally only carries shapes that
// repeat across panels; goal-name's constraint (required, 1–128
// chars) lives nowhere else.
function goalNameError(value: string): string | null {
  const v = value.trim();
  if (v.length === 0) return 'Goal name is required (up to 128 characters).';
  if (v.length > 128) return 'Goal name is required (up to 128 characters).';
  return null;
}

function NewGoalForm({
  siteID,
  onCreated,
  onError,
}: {
  siteID: number;
  onCreated: () => void | Promise<void>;
  onError: (msg: string) => void;
}) {
  const [name, setName] = useState('');
  const [pattern, setPattern] = useState('');
  const [value, setValue] = useState(0);
  const [busy, setBusy] = useState(false);
  const [touched, setTouched] = useState<{ name: boolean; pattern: boolean; value: boolean }>({
    name: false,
    pattern: false,
    value: false,
  });

  // Validators run on every keystroke so the Save button can disable
  // itself the moment the form is invalid. The user only sees the
  // error sentence after they have interacted with the field
  // (`touched`) so a fresh form is not red-on-arrival.
  const nameError = useMemo(() => goalNameError(name), [name]);
  const patternError = useMemo(() => validators.eventName(pattern), [pattern]);
  const valueError = useMemo(() => validators.goalValue(value), [value]);
  const formInvalid = nameError !== null || patternError !== null || valueError !== null;

  async function onSubmit(ev: Event) {
    ev.preventDefault();
    if (busy || formInvalid) return;
    setBusy(true);
    try {
      await createGoal(siteID, {
        name: name.trim(),
        match_type: 'event_name_equals',
        pattern,
        value,
        enabled: true,
      });
      setName('');
      setPattern('');
      setValue(0);
      setTouched({ name: false, pattern: false, value: false });
      await onCreated();
    } catch (e) {
      onError(errorMessage(e, "Couldn't create the goal. Try again."));
    } finally {
      setBusy(false);
    }
  }

  const showName = touched.name && nameError !== null;
  const showPattern = touched.pattern && patternError !== null;
  const showValue = touched.value && valueError !== null;

  return (
    <form class="statnive-admin-new" onSubmit={onSubmit} noValidate>
      <h3>New goal</h3>

      <label class="statnive-admin-field">
        <span>Name</span>
        <input
          type="text"
          required
          maxLength={128}
          class={showName ? 'is-invalid' : undefined}
          aria-invalid={showName ? 'true' : 'false'}
          value={name}
          onInput={(e) => setName((e.target as HTMLInputElement).value)}
          onBlur={() => setTouched((t) => ({ ...t, name: true }))}
        />
        <p class="statnive-admin-modal-helper">
          What you&apos;ll see this goal called in reports. Free text.
        </p>
        {showName ? (
          <p class="statnive-admin-field-error" role="alert">{nameError}</p>
        ) : null}
      </label>

      <label class="statnive-admin-field">
        <span>Event name (exact match)</span>
        <input
          type="text"
          required
          maxLength={128}
          class={showPattern ? 'is-invalid' : undefined}
          aria-invalid={showPattern ? 'true' : 'false'}
          value={pattern}
          onInput={(e) => setPattern((e.target as HTMLInputElement).value)}
          onBlur={() => setTouched((t) => ({ ...t, pattern: true }))}
        />
        <p class="statnive-admin-modal-helper">
          The exact <code>event_name</code> your tracker sends. For example <code>signup</code> or <code>add_to_cart</code>. Must match what <code>window.statniveLive.track(...)</code> posts.
        </p>
        {showPattern ? (
          <p class="statnive-admin-field-error" role="alert">{patternError}</p>
        ) : null}
      </label>

      <label class="statnive-admin-field">
        <span>Value</span>
        <input
          type="number"
          min={0}
          class={showValue ? 'is-invalid' : undefined}
          aria-invalid={showValue ? 'true' : 'false'}
          value={value}
          onInput={(e) => setValue(Number((e.target as HTMLInputElement).value))}
          onBlur={() => setTouched((t) => ({ ...t, value: true }))}
        />
        <p class="statnive-admin-modal-helper">
          Revenue this goal is worth. <strong>0</strong> means the tracker&apos;s own value flows through (use this for e-commerce purchases). <strong>&gt; 0</strong> means every goal hit counts the same amount.
        </p>
        {showValue ? (
          <p class="statnive-admin-field-error" role="alert">{valueError}</p>
        ) : null}
      </label>

      <button type="submit" disabled={busy || formInvalid}>
        {busy ? 'Creating…' : 'Create goal'}
      </button>
    </form>
  );
}
