import { Icon } from "../components/Icon";
import { Notice } from "../components/States";

// Fixed reason codes from /auth/callback (api/console.go signinError).
// Unknown codes get the generic message: nothing from the URL is shown.
const reasons: Record<string, string> = {
  provider_unavailable: "The identity provider isn't ready yet. Try again in a minute. API keys keep working meanwhile.",
  expired: "That sign-in took too long or was already used. Start again.",
  invalid_request: "The sign-in couldn't be verified. Start again.",
  denied: "The sign-in was cancelled or refused at the identity provider.",
};

/** The sign-in page. `returnTo` is where to land afterwards (validated server side too). */
export function SignIn({ returnTo, error, ended }: { returnTo: string; error?: string | null; ended?: boolean }) {
  const message = error ? (reasons[error] ?? reasons.invalid_request) : ended ? "Your session ended. Sign in again to continue." : undefined;
  return (
    <main className="signin" id="main">
      <div className="signin-card">
        <div className="rail-brand page-title-row">
          <Icon name="layers" size={20} />
          <h1 className="page-title">Serendip</h1>
        </div>
        {message && <Notice tone={error ? "danger" : "warn"}>{message}</Notice>}
        <p className="state-body">Sign in with your Serendip identity to manage applications on this install.</p>
        <div>
          {/* A full navigation, not a fetch: the login happens at the identity provider. */}
          <a className="button button--primary" href={`/auth/login?return=${encodeURIComponent(returnTo)}`}>
            Sign in
          </a>
        </div>
      </div>
    </main>
  );
}
