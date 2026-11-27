import { useEffect, useState } from "react";
import { onTrayNavigate, type TrayNavigateTarget } from "../tauri";
import SyncRootsTab from "../components/SyncRootsTab";
import ConflictsTab from "../components/ConflictsTab";
import SettingsTab from "../components/SettingsTab";
import AppHeader from "../components/AppHeader";
import TransferSpeedBar from "../components/TransferSpeedBar";

/** The tray can navigate to any of these, so they are the same set. */
type Page = TrayNavigateTarget;

export default function DashboardPage() {
  const [page, setPage] = useState<Page>("sync");

  // The tray context menu can jump to a page via the tray_navigate event.
  useEffect(() => {
    const unlisten = onTrayNavigate(setPage);
    return () => { unlisten.then((fn) => fn()); };
  }, []);

  return (
    <div className="flex h-screen flex-col bg-background">
      <AppHeader
        page={page}
        onNavigate={setPage}
        onAfterWipe={() => setPage("sync")}
      />

      <main className="flex-1 overflow-y-auto p-4">
        {page === "sync"      && <SyncRootsTab />}
        {page === "conflicts" && <ConflictsTab />}
        {page === "settings"  && <SettingsTab />}
      </main>

      <TransferSpeedBar />
    </div>
  );
}
