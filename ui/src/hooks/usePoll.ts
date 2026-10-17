// Live-enough status without a watch API (spec 016 plan.md decision 12):
// poll on an interval, pause while the tab is hidden, back off on errors,
// and cancel the in-flight request when the screen goes away.
import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, isShown, type NetworkError } from "../api/client";

const MAX_BACKOFF_MS = 60_000;

export interface Poll<T> {
  data: T | undefined;
  error: ApiError | NetworkError | undefined;
  loading: boolean;
  updatedAt: Date | undefined;
  refresh: () => void;
}

export function usePoll<T>(load: (signal: AbortSignal) => Promise<T>, intervalMs: number, key: string): Poll<T> {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<ApiError | NetworkError>();
  const [updatedAt, setUpdatedAt] = useState<Date>();
  const [tick, setTick] = useState(0);
  const loadRef = useRef(load);
  loadRef.current = load;

  // A new key is a different resource: start clean.
  useEffect(() => {
    setData(undefined);
    setError(undefined);
    setUpdatedAt(undefined);
  }, [key]);

  useEffect(() => {
    const ctrl = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let failures = 0;
    let stopped = false;

    async function run() {
      timer = undefined;
      if (stopped) return;
      if (document.hidden) return; // resumed by the visibilitychange handler
      try {
        const value = await loadRef.current(ctrl.signal);
        if (stopped) return;
        failures = 0;
        setData(value);
        setError(undefined);
        setUpdatedAt(new Date());
      } catch (err) {
        if (stopped || ctrl.signal.aborted) return;
        if (!isShown(err)) throw err;
        failures++;
        setError(err);
        if (err instanceof ApiError && err.status === 401) return; // the session gate takes over
      }
      schedule();
    }

    function schedule() {
      if (stopped) return;
      let delay = intervalMs;
      if (failures > 0) delay = Math.min(MAX_BACKOFF_MS, intervalMs * 2 ** failures);
      timer = setTimeout(run, delay);
    }

    function onVisibility() {
      if (!document.hidden && timer === undefined && !stopped) void run();
      if (document.hidden && timer !== undefined) {
        clearTimeout(timer);
        timer = undefined;
      }
    }

    document.addEventListener("visibilitychange", onVisibility);
    void run();
    return () => {
      stopped = true;
      ctrl.abort();
      if (timer !== undefined) clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [intervalMs, key, tick]);

  const refresh = useCallback(() => setTick((t) => t + 1), []);
  return { data, error, loading: data === undefined && error === undefined, updatedAt, refresh };
}

/** Re-renders every few seconds so relative times ("12 s ago") stay true. */
export function useNow(everyMs = 5_000): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), everyMs);
    return () => clearInterval(id);
  }, [everyMs]);
  return now;
}
