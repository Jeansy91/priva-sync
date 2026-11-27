import { useEffect, useRef, useState } from "react";
import { AlertTriangle, FolderSync, LogOut, Settings, Trash2, User } from "lucide-react";
import { useTranslation } from "@privasync/i18n";
import { cmd } from "../tauri";
import { useAppStore } from "../store";

type Page = "sync" | "conflicts" | "settings";

interface Props {
  page:        Page;
  onNavigate:  (page: Page) => void;
  onAfterWipe: () => void;
}

/** Strips scheme and default ports — `http://localhost:8080/` -> `localhost:8080`. */
function formatServerUrl(raw: string): string {
  try {
    const u = new URL(raw);
    const isDefaultPort =
      (u.protocol === "https:" && (u.port === "" || u.port === "443")) ||
      (u.protocol === "http:"  && (u.port === "" || u.port === "80"));
    return isDefaultPort ? u.hostname : `${u.hostname}:${u.port}`;
  } catch {
    return raw;
  }
}

function Avatar({ blobUrl, username, size }: { blobUrl: string | null; username: string; size: number }) {
  const initials = (username || "?").slice(0, 2).toUpperCase();
  const dim = { width: size, height: size };
  if (blobUrl) {
    return (
      <img
        src={blobUrl}
        alt={username}
        className="rounded-full object-cover bg-muted"
        style={dim}
      />
    );
  }
  return (
    <div
      className="rounded-full bg-blue-100 dark:bg-blue-900/40 flex items-center justify-center text-xs font-semibold text-blue-700 dark:text-blue-300"
      style={dim}
      aria-label={username || "User"}
    >
      {initials || <User className="h-4 w-4" />}
    </div>
  );
}

export default function AppHeader({ page, onNavigate, onAfterWipe }: Props) {
  const { t } = useTranslation();
  const { config, status, pathWarnings, setConfig } = useAppStore();
  const [blobUrl, setBlobUrl] = useState<string | null>(null);
  const [menuOpen, setMenuOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const menuRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    let createdUrl: string | null = null;
    setBlobUrl((prev) => { if (prev) URL.revokeObjectURL(prev); return null; });

    if (config?.is_logged_in) {
      cmd.fetchMyAvatar().then((bytes) => {
        if (cancelled || !bytes || bytes.length === 0) return;
        const blob = new Blob([new Uint8Array(bytes)], { type: "image/png" });
        createdUrl = URL.createObjectURL(blob);
        setBlobUrl(createdUrl);
      }).catch(() => {});
    }
    return () => {
      cancelled = true;
      if (createdUrl) URL.revokeObjectURL(createdUrl);
    };
  }, [config?.is_logged_in, config?.username]);

  useEffect(() => {
    if (!menuOpen) return;
    const onDoc = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenuOpen(false);
      }
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [menuOpen]);

  async function handleLogout() {
    setMenuOpen(false);
    if (!confirm(t("desktop.header.logoutConfirm"))) return;
    setBusy(true);
    try {
      await cmd.logout();
      const cfg = await cmd.getConfig();
      setConfig(cfg);
    } finally {
      setBusy(false);
    }
  }

  async function handleWipe() {
    setMenuOpen(false);
    if (!confirm(t("desktop.header.wipeConfirm"))) return;
    setBusy(true);
    try {
      await cmd.wipeAccountLocal();
      const cfg = await cmd.getConfig();
      setConfig(cfg);
      onAfterWipe();
    } finally {
      setBusy(false);
    }
  }

  const issueCount = (status?.conflict_count ?? 0) + pathWarnings.length;
  const serverLabel = config?.server_url ? formatServerUrl(config.server_url) : "";

  const actions: { id: Page; label: string; icon: React.ReactNode; badge?: number }[] = [
    { id: "sync",      label: t("desktop.header.nav.sync"),      icon: <FolderSync className="h-4 w-4" /> },
    { id: "conflicts", label: t("desktop.header.nav.conflicts"), icon: <AlertTriangle className="h-4 w-4" />, badge: issueCount },
    { id: "settings",  label: t("desktop.header.nav.settings"),  icon: <Settings className="h-4 w-4" /> },
  ];

  return (
    <header className="flex items-center justify-between border-b border-border bg-card px-3 py-2">
      <div className="relative" ref={menuRef}>
        <button
          onClick={() => setMenuOpen((v) => !v)}
          disabled={busy || !config?.is_logged_in}
          className="flex items-center gap-2 rounded-md px-1.5 py-1 hover:bg-accent disabled:opacity-50"
          aria-haspopup="menu"
          aria-expanded={menuOpen}
        >
          <Avatar blobUrl={blobUrl} username={config?.username ?? ""} size={32} />
          <div className="text-left leading-tight">
            <p className="text-sm font-medium text-foreground">{config?.username || "—"}</p>
            <p className="text-[11px] text-muted-foreground/70" title={config?.server_url}>
              {serverLabel || "—"}
            </p>
          </div>
        </button>

        {menuOpen && (
          <div
            role="menu"
            className="absolute left-0 top-full z-30 mt-1 min-w-56 overflow-hidden rounded-md border border-border bg-card py-1 shadow-lg"
          >
            <button
              role="menuitem"
              onClick={handleLogout}
              className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-foreground hover:bg-accent/50"
            >
              <LogOut className="h-4 w-4 text-muted-foreground" />
              {t("desktop.header.menu.logout")}
            </button>
            <button
              role="menuitem"
              onClick={handleWipe}
              className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/40"
            >
              <Trash2 className="h-4 w-4" />
              {t("desktop.header.menu.wipe")}
            </button>
          </div>
        )}
      </div>

      <div className="flex items-center gap-1">
        {actions.map((a) => {
          const isActive = page === a.id;
          return (
            <button
              key={a.id}
              onClick={() => onNavigate(a.id)}
              title={a.label}
              className={`relative flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-medium transition-colors
                ${isActive ? "bg-blue-50 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400" : "text-muted-foreground hover:bg-accent hover:text-foreground"}`}
            >
              {a.icon}
              <span className="hidden sm:inline">{a.label}</span>
              {(a.badge ?? 0) > 0 && (
                <span className="ml-0.5 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-red-500 px-1 text-[10px] font-bold text-white">
                  {a.badge}
                </span>
              )}
            </button>
          );
        })}
      </div>
    </header>
  );
}
