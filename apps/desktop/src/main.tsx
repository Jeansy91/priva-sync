import React from "react";
import ReactDOM from "react-dom/client";
import { setupI18n } from "@privasync/i18n";
import { initTheme } from "@privasync/ui";
import { cmd } from "./tauri";
import { bootLanguageCandidates } from "./i18n/bootLanguage";
import { installContextMenuGuard } from "./contextMenuGuard";
import App from "./App";
import "./index.css";

installContextMenuGuard();

// Apply the persisted theme before first paint to avoid a light flash.
initTheme();

// Pick the language before the first render: the persisted LocalDb value
// wins, otherwise we fall back to the browser/OS locales (client
// follows OS, fallback EN). Tauri invoke is available right after webview
// boot.
async function bootstrap() {
  let persisted: string | null = null;
  try {
    persisted = await cmd.getLanguage();
  } catch {
    // Tauri bridge unavailable (e.g. SSR/tests) — browser locale is enough.
  }
  const i18n = setupI18n(bootLanguageCandidates(
    persisted,
    navigator.languages ?? [],
    navigator.language ?? "",
  ));

  // Mirror the resolved language into LocalDb. The tray menu is rendered in
  // Rust and can only read the database — without this it stayed English for
  // everyone who never opened the language picker, because the OS-locale
  // fallback above lives here and nowhere else. Kept under a separate key so
  // an explicit choice stays distinguishable from "this is what the OS said
  // today" and the app keeps following the OS locale.
  try {
    await cmd.setEffectiveLanguage(i18n.language?.split("-")[0] ?? "en");
  } catch {
    // Tray stays on its previous language — not worth blocking the boot for.
  }

  ReactDOM.createRoot(document.getElementById("root")!).render(
    <React.StrictMode>
      <App />
    </React.StrictMode>
  );
}

bootstrap();
