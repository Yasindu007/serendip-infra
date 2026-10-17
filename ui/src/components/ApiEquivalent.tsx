// Interface parity made visible (constitution 7, spec 016): the exact request
// a screen sends, so a person, a script or an AI can repeat it. Only method,
// path and JSON body are shown; headers (and so credentials) never are.

export interface ApiRequest {
  method: "GET" | "POST" | "PUT";
  path: string;
  body?: unknown;
}

export function formatRequest(req: ApiRequest): string {
  const line = `${req.method} ${req.path}`;
  if (req.body === undefined) return line;
  return `${line}\nContent-Type: application/json\n\n${JSON.stringify(req.body, null, 2)}`;
}

export function ApiEquivalent({ request, open = false }: { request: ApiRequest; open?: boolean }) {
  return (
    <details className="api-eq" open={open}>
      <summary>Same request through the API</summary>
      <pre translate="no">{formatRequest(request)}</pre>
    </details>
  );
}
