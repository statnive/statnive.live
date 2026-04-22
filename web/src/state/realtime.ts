import { signal } from '@preact/signals';

// realtimeTickSignal increments every 10s while the page is visible,
// pauses when document.hidden flips to true, and resumes (with an
// immediate tick) on visibility restore. Panels subscribe to this
// signal to trigger refetches without owning their own timers.
//
// Mounted once at module load per `client-event-listeners`. No matter
// how many Realtime panel instances exist in the tree, the listener
// registry stays at one.

export const REALTIME_INTERVAL_MS = 10_000;

export const realtimeTickSignal = signal<number>(0);

let intervalHandle: ReturnType<typeof setInterval> | null = null;

function tick(): void {
  realtimeTickSignal.value = realtimeTickSignal.value + 1;
}

function start(): void {
  if (intervalHandle != null) return;
  intervalHandle = setInterval(tick, REALTIME_INTERVAL_MS);
}

function stop(): void {
  if (intervalHandle == null) return;
  clearInterval(intervalHandle);
  intervalHandle = null;
}

if (typeof document !== 'undefined') {
  if (!document.hidden) start();

  document.addEventListener('visibilitychange', () => {
    if (document.hidden) {
      stop();
    } else {
      tick();
      start();
    }
  });
}
