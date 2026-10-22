import { useEffect, useState } from "react";
import { Api, type Me } from "./api";
import { auth0Login, devLogin, loadConfig, logout, restore, type PublicConfig } from "./auth";
import { Orders } from "./pages/Orders";
import { OrderDetail } from "./pages/OrderDetail";
import { Fulfillment } from "./pages/Fulfillment";
import { Inventory } from "./pages/Inventory";
import { Audit } from "./pages/Audit";

const DEV_USERS = [
  ["ana.admin@orderflow.dev", "admin"],
  ["sam.support@orderflow.dev", "support"],
  ["vera.viewer@orderflow.dev", "viewer"],
];

type Page = { name: "orders" } | { name: "order"; id: string } | { name: "fulfillment" } | { name: "inventory" } | { name: "audit" };

export function App() {
  const [cfg, setCfg] = useState<PublicConfig>();
  const [api, setApi] = useState<Api>();
  const [me, setMe] = useState<Me>();
  const [page, setPage] = useState<Page>({ name: "orders" });
  const [err, setErr] = useState("");

  const start = async (token: string | null) => {
    if (!token) return;
    const a = new Api(token);
    try {
      setMe(await a.me());
      setApi(a);
    } catch {
      sessionStorage.clear();
    }
  };

  useEffect(() => {
    loadConfig().then(async (c) => {
      setCfg(c);
      await start(await restore(c));
    });
  }, []);

  if (!cfg) return <p className="pad">Loading…</p>;

  if (!api || !me)
    return (
      <div className="login">
        <h1>orderflow backoffice</h1>
        {cfg.authMode === "auth0" ? (
          <button onClick={() => auth0Login()}>Log in with Auth0</button>
        ) : (
          <>
            <p className="muted">Dev mode: pick a demo identity (tokens come from the local OIDC issuer).</p>
            {DEV_USERS.map(([email, role]) => (
              <button key={email} onClick={() => devLogin(cfg, email).then(start).catch((e) => setErr(String(e)))}>
                {email} <span className={`badge ${role}`}>{role}</span>
              </button>
            ))}
          </>
        )}
        {err && <p className="error">{err}</p>}
      </div>
    );

  const can = (p: string) => me.permissions.includes(p);
  const nav = (p: Page, label: string, perm: string) =>
    can(perm) && (
      <button className={page.name === p.name ? "active" : ""} onClick={() => setPage(p)}>
        {label}
      </button>
    );

  return (
    <div>
      <header>
        <strong>orderflow</strong>
        <nav>
          {nav({ name: "orders" }, "Orders", "orders:read")}
          {nav({ name: "fulfillment" }, "Fulfillment", "shipments:read")}
          {nav({ name: "inventory" }, "Inventory", "inventory:read")}
          {nav({ name: "audit" }, "Audit log", "audit:read")}
        </nav>
        <span className="who">
          {me.principal.email ?? me.principal.sub}{" "}
          {me.principal.roles.map((r) => (
            <span key={r} className={`badge ${r}`}>
              {r}
            </span>
          ))}
          <button className="link" onClick={() => logout(cfg)}>
            log out
          </button>
        </span>
      </header>
      <main>
        {page.name === "orders" && <Orders api={api} open={(id) => setPage({ name: "order", id })} />}
        {page.name === "order" && <OrderDetail api={api} id={page.id} can={can} back={() => setPage({ name: "orders" })} />}
        {page.name === "fulfillment" && <Fulfillment api={api} can={can} open={(id) => setPage({ name: "order", id })} />}
        {page.name === "inventory" && <Inventory api={api} can={can} />}
        {page.name === "audit" && <Audit api={api} />}
      </main>
    </div>
  );
}
