const relative = new Intl.RelativeTimeFormat(undefined, { numeric: "auto", style: "short" });

/** "4 min ago", "now", "2 days ago". Unparseable input is returned as-is. */
export function formatAge(iso: string, now: Date): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return iso;
  const seconds = Math.round((then - now.getTime()) / 1000);
  const abs = Math.abs(seconds);
  if (abs < 45) return relative.format(0, "second");
  if (abs < 3600) return relative.format(Math.round(seconds / 60), "minute");
  if (abs < 86400) return relative.format(Math.round(seconds / 3600), "hour");
  return relative.format(Math.round(seconds / 86400), "day");
}
