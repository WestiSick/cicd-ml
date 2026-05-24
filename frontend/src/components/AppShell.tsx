import { NavLink, Outlet, useLocation } from "react-router-dom";
import {
  LayoutDashboard,
  History as HistoryIcon,
  Database,
  FlaskConical,
  Activity,
  Settings,
} from "lucide-react";

import { HealthDot } from "./HealthDot";
import { LanguageSwitcher, useT } from "@/i18n";
import styles from "./AppShell.module.css";

/* AppShell is the persistent chrome around all authenticated pages.
 *
 * Layout follows the page template in docs/architecture.md:
 *   ┌─ TopBar (mono breadcrumb · health · lang) ─┐
 *   │ Sidebar │ Page content                     │
 *   └─────────┴──────────────────────────────────┘
 *
 * The breadcrumb is monospace by design — it reinforces the "instrument"
 * aesthetic and gives the eye a stable anchor for path comparisons.
 *
 * Language switcher is mounted in two places:
 *   - top bar (compact EN/RU pill) for one-click access from any page
 *   - sidebar footer for completeness, paired with the version label
 */
export function AppShell() {
  const t = useT();
  const location = useLocation();
  const crumb = location.pathname === "/" ? "/dashboard" : location.pathname;

  return (
    <div className={styles.shell}>
      <header className={styles.topbar}>
        <div className={styles.brand}>
          <span className={styles.brandMark}>cicd-ml</span>
          <span className={styles.brandSep}>·</span>
          <span className={styles.crumb}>{crumb}</span>
        </div>
        <div className={styles.topbarRight}>
          <LanguageSwitcher compact />
          <HealthDot />
        </div>
      </header>

      <aside className={styles.sidebar}>
        <nav className={styles.nav}>
          <SidebarLink to="/dashboard"   icon={<LayoutDashboard size={14} strokeWidth={1.5} />} label={t("nav.dashboard")} />
          <SidebarLink to="/history"     icon={<HistoryIcon size={14} strokeWidth={1.5} />}     label={t("nav.history")} />
          <SidebarLink to="/datasets"    icon={<Database size={14} strokeWidth={1.5} />}        label={t("nav.datasets")} />
          <SidebarLink to="/experiments" icon={<FlaskConical size={14} strokeWidth={1.5} />}    label={t("nav.experiments")} />
          <SidebarLink to="/simulator"   icon={<Activity size={14} strokeWidth={1.5} />}        label={t("nav.simulator")} />
          <div className={styles.navSpacer} />
          <SidebarLink to="/admin"       icon={<Settings size={14} strokeWidth={1.5} />}        label={t("nav.admin")} />
        </nav>
        <div className={styles.sidebarFooter}>
          <span className="mono">v0.1.0</span>
        </div>
      </aside>

      <main className={styles.main}>
        <div className={styles.mainInner}>
          <Outlet />
        </div>
      </main>
    </div>
  );
}

function SidebarLink({
  to,
  icon,
  label,
}: {
  to: string;
  icon: React.ReactNode;
  label: string;
}) {
  return (
    <NavLink
      to={to}
      className={({ isActive }) =>
        [styles.navLink, isActive ? styles.navLinkActive : ""].join(" ")
      }
    >
      <span className={styles.navIcon}>{icon}</span>
      <span>{label}</span>
    </NavLink>
  );
}
