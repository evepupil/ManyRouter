import { Dialog } from "@base-ui/react/dialog";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import {
  Activity,
  Building2,
  CircleDollarSign,
  ClipboardList,
  GitBranch,
  History,
  LayoutList,
  Network,
  Server,
  X,
} from "lucide-react";
import { IconButton } from "../components/ui";

const links = [
  { path: "/sites", key: "sites", icon: Building2 },
  { path: "/suppliers", key: "suppliers", icon: Server },
  { path: "/deployments", key: "deployments", icon: Network },
  { path: "/auto", key: "auto", icon: GitBranch },
  { path: "/pricing", key: "pricing", icon: CircleDollarSign },
  { path: "/plans", key: "plans", icon: History },
  { path: "/observability", key: "observability", icon: Activity },
  { path: "/operations", key: "operations", icon: LayoutList },
  { path: "/audit", key: "audit", icon: ClipboardList },
] as const;

function NavigationLinks({ onNavigate }: { onNavigate?: () => void }) {
  const { t } = useTranslation();
  return (
    <nav aria-label="主导航" className="navigation">
      {links.map(({ path, key, icon: Icon }) => (
        <Link key={path} to={path} className="nav-link" onClick={onNavigate}>
          <Icon aria-hidden="true" />
          <span>{t(key)}</span>
        </Link>
      ))}
    </nav>
  );
}

function Brand() {
  return (
    <div className="brand">
      <img src="/favicon.svg" alt="" />
      <span>ManyRouter</span>
    </div>
  );
}

export function DesktopNavigation() {
  return (
    <aside className="sidebar desktop-navigation">
      <Brand />
      <NavigationLinks />
    </aside>
  );
}

export function MobileNavigation({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange} modal>
      <Dialog.Portal>
        <Dialog.Backdrop className="nav-backdrop" />
        <Dialog.Popup
          id="mobile-navigation"
          className="sidebar mobile-navigation"
          finalFocus={() => document.getElementById("mobile-menu-button")}
        >
          <Dialog.Title className="sr-only">主导航</Dialog.Title>
          <div className="mobile-navigation-heading">
            <Brand />
            <IconButton label="关闭导航" onClick={() => onOpenChange(false)}>
              <X size={18} />
            </IconButton>
          </div>
          <NavigationLinks onNavigate={() => onOpenChange(false)} />
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
