import { createAuth0Client, type Auth0Client } from "@auth0/auth0-spa-js";

export type PublicConfig = {
  authMode: "dev" | "auth0";
  devIssuer: string;
  auth0Domain: string;
  auth0ClientId: string;
  auth0Audience: string;
};

export type Session = { token: string; logout: () => void };

const KEY = "orderflow.devtoken";
let auth0: Auth0Client | null = null;

export async function loadConfig(): Promise<PublicConfig> {
  const r = await fetch("/config.json");
  return r.json();
}

/** Dev mode: the local issuer mints a token for a demo identity. */
export async function devLogin(cfg: PublicConfig, email: string): Promise<string> {
  const r = await fetch(`${cfg.devIssuer}/token`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email }),
  });
  if (!r.ok) throw new Error("dev login failed");
  const { access_token } = await r.json();
  sessionStorage.setItem(KEY, access_token);
  return access_token;
}

export async function restore(cfg: PublicConfig): Promise<string | null> {
  if (cfg.authMode === "dev") return sessionStorage.getItem(KEY);
  auth0 = await createAuth0Client({
    domain: cfg.auth0Domain,
    clientId: cfg.auth0ClientId,
    authorizationParams: { audience: cfg.auth0Audience, redirect_uri: window.location.origin },
    cacheLocation: "memory",
  });
  if (window.location.search.includes("code=")) {
    await auth0.handleRedirectCallback();
    window.history.replaceState({}, "", "/");
  }
  if (await auth0.isAuthenticated()) return (await auth0.getTokenSilently()) ?? null;
  return null;
}

export async function auth0Login() {
  await auth0?.loginWithRedirect();
}

export function logout(cfg: PublicConfig) {
  sessionStorage.removeItem(KEY);
  if (cfg.authMode === "auth0") auth0?.logout({ logoutParams: { returnTo: window.location.origin } });
  else window.location.reload();
}
