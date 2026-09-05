import { useEffect, useState } from "react";
import { Outlet } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { LogOut, Menu, RefreshCw } from "lucide-react";
import { IconButton, Notice, Select } from "../components/ui";
import { useLogout, useSession } from "../features/auth/auth";
import { useScope } from "./scope";
import { DesktopNavigation, MobileNavigation } from "./navigation";

export function Shell() {
  const { t } = useTranslation();
  const session = useSession();
  const logout = useLogout();
  const { siteId, setSiteId, sites, state, retry, fetching } = useScope();
  const [menuOpen, setMenuOpen] = useState(false);
  useEffect(() => {
    const desktop = window.matchMedia("(min-width: 961px)");
    const closeOnDesktop = () => {
      if (desktop.matches) setMenuOpen(false);
    };
    desktop.addEventListener("change", closeOnDesktop);
    return () => desktop.removeEventListener("change", closeOnDesktop);
  }, []);
  return (
    <div className="app">
      <DesktopNavigation />
      <MobileNavigation open={menuOpen} onOpenChange={setMenuOpen} />
      <div className="workspace">
        <header className="topbar">
          <div className="topbar-start">
            <IconButton
              label="打开导航"
              id="mobile-menu-button"
              aria-expanded={menuOpen}
              aria-controls="mobile-navigation"
              aria-haspopup="dialog"
              className="button button-quiet button-icon mobile-menu"
              onClick={() => setMenuOpen((value) => !value)}
            >
              <Menu size={18} />
            </IconButton>
            <Select
              className="site-switch"
              aria-label="当前站点"
              value={siteId}
              disabled={
                state === "loading" || state === "error" || state === "empty"
              }
              aria-invalid={state === "error"}
              onChange={(event) => setSiteId(event.target.value)}
            >
              <option value="">
                {state === "loading"
                  ? "正在读取站点"
                  : state === "error"
                    ? "站点读取失败"
                    : state === "empty"
                      ? "尚未接入站点"
                      : t("allSites")}
              </option>
              {sites.map((site) => (
                <option key={site.id} value={site.id}>
                  {site.name}
                </option>
              ))}
            </Select>
            {state === "error" && (
              <IconButton
                label="重新读取站点"
                onClick={retry}
                disabled={fetching}
              >
                <RefreshCw size={17} />
              </IconButton>
            )}
          </div>
          <div className="topbar-end">
            <span className="account-name">{session.user.username}</span>
            <IconButton
              label={t("logout")}
              onClick={() => logout.mutate()}
              disabled={logout.isPending}
            >
              <LogOut size={17} />
            </IconButton>
          </div>
        </header>
        <main className="content">
          <Notice error={logout.error} />
          <Outlet />
        </main>
      </div>
    </div>
  );
}
