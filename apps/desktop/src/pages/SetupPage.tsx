import { useEffect, useRef, useState } from "react";
import { CheckCircle2, Loader2, Lock, Server, Settings, User, XCircle } from "lucide-react";
import { Button, Input, OtpInput } from "@privasync/ui";
import { useTranslation } from "@privasync/i18n";
import { cmd } from "../tauri";
import { commandErrorMessage } from "../commandError";
import PreLoginLanguagePicker from "../components/PreLoginLanguagePicker";

interface Props {
  onLogin: () => void;
  /** Last persisted server URL/username, prefilled into the login form ("" if never set). */
  initialServerUrl: string;
  initialUsername:  string;
  /** Opens the settings page — everything in it is local, so it works without a session. */
  onOpenSettings: () => void;
}

/** Shared page chrome (logo, language picker, settings, subtitle) for both login steps. */
function SetupShell({
  subtitle, onOpenSettings, children,
}: {
  subtitle: string;
  onOpenSettings: () => void;
  children: React.ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <div className="relative flex h-screen flex-col items-center justify-center bg-background px-4">
      <div className="absolute right-4 top-4 flex items-center gap-2">
        <PreLoginLanguagePicker />
        <button
          type="button"
          onClick={onOpenSettings}
          aria-label={t("desktop.header.nav.settings")}
          title={t("desktop.header.nav.settings")}
          className="rounded-lg p-1.5 text-muted-foreground/70 hover:bg-accent/50 hover:text-foreground"
        >
          <Settings className="h-4 w-4" />
        </button>
      </div>
      <div className="mb-8 flex flex-col items-center gap-2">
        <img src="/logo.png" alt="PrivaSync" className="h-20 w-20" />
        <h1 className="text-2xl font-bold text-foreground">PrivaSync</h1>
        <p className="text-sm text-muted-foreground">{subtitle}</p>
      </div>
      {children}
    </div>
  );
}

export default function SetupPage({ onLogin, initialServerUrl, initialUsername, onOpenSettings }: Props) {
  const { t } = useTranslation();
  const [serverUrl, setServerUrl] = useState(initialServerUrl);
  const [username, setUsername]   = useState(initialUsername);
  const [password, setPassword]   = useState("");
  const [error, setError]         = useState("");
  const [loading, setLoading]     = useState(false);

  // Instance check on the server-URL field: debounced so it doesn't fire on
  // every keystroke, and cached per validated URL so re-focusing the field
  // without changing its value doesn't re-trigger the update check below.
  const [checkState, setCheckState] = useState<"idle" | "checking" | "ok" | "invalid">("idle");
  const updateCheckedUrlRef = useRef<string | null>(null);

  // Set once the server reports the account has 2FA enabled — the login
  // command deliberately withholds the access token until verifyTotp
  // exchanges this session_token for a real one.
  const [sessionToken, setSessionToken] = useState<string | null>(null);
  const [code, setCode]           = useState("");
  const [useBackup, setUseBackup] = useState(false);

  // Live instance check while typing the server URL, before Bob commits to
  // logging in. Only on the URL step — once a 2FA session_token exists the
  // URL is already confirmed working, no reason to re-check it.
  useEffect(() => {
    if (sessionToken) return;
    const trimmed = serverUrl.trim();
    if (!trimmed) {
      setCheckState("idle");
      return;
    }
    setCheckState("checking");
    const timer = setTimeout(() => {
      cmd.verifyInstance(trimmed)
        .then((ok) => {
          setCheckState(ok ? "ok" : "invalid");
          if (ok && updateCheckedUrlRef.current !== trimmed) {
            updateCheckedUrlRef.current = trimmed;
            void cmd.startAutoUpdateFor(trimmed);
          }
        })
        .catch(() => setCheckState("invalid"));
    }, 500);
    return () => clearTimeout(timer);
  }, [serverUrl, sessionToken]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setLoading(true);
    try {
      const outcome = await cmd.login(serverUrl.trim(), username.trim(), password);
      if (outcome.requires_2fa && outcome.session_token) {
        setSessionToken(outcome.session_token);
      } else {
        onLogin();
      }
    } catch (err) {
      setError(commandErrorMessage(err));
    } finally {
      setLoading(false);
    }
  }

  async function verify(codeOverride?: string) {
    if (!sessionToken) return;
    setError("");
    setLoading(true);
    try {
      await cmd.verifyTotp(sessionToken, codeOverride ?? code, useBackup);
      onLogin();
    } catch (err) {
      setError(commandErrorMessage(err));
    } finally {
      setLoading(false);
    }
  }

  function handleVerify(e: React.FormEvent) {
    e.preventDefault();
    void verify();
  }

  function handleBackToLogin() {
    setSessionToken(null);
    setCode("");
    setUseBackup(false);
    setError("");
  }

  if (sessionToken) {
    return (
      <SetupShell subtitle={t("desktop.setup.totpSubtitle")} onOpenSettings={onOpenSettings}>
        <form
          onSubmit={handleVerify}
          className="w-full max-w-sm space-y-4 rounded-xl bg-card p-6 shadow-md"
        >
          <div>
            <label className="mb-1 block text-xs font-medium text-muted-foreground">
              {useBackup ? t("desktop.setup.backupCode") : t("desktop.setup.totpCode")}
            </label>
            {useBackup ? (
              <Input
                type="text"
                required
                autoFocus
                value={code}
                onChange={(e) => setCode(e.target.value.trim())}
                placeholder={t("desktop.setup.backupCodePlaceholder")}
                className="rounded-lg py-2 h-auto"
              />
            ) : (
              <OtpInput
                value={code}
                onChange={setCode}
                onComplete={(finalCode) => void verify(finalCode)}
                autoFocus
                hasError={!!error}
                ariaLabel={t("desktop.setup.totpCode")}
                className="justify-center"
              />
            )}
          </div>

          {error && (
            <p className="rounded-lg bg-red-50 dark:bg-red-950/40 px-3 py-2 text-xs text-red-700 dark:text-red-300">
              {error}
            </p>
          )}

          <Button type="submit" disabled={loading} className="w-full rounded-lg">
            {loading ? t("desktop.setup.submitting") : t("desktop.setup.totpSubmit")}
          </Button>

          <button
            type="button"
            onClick={() => { setUseBackup((v) => !v); setCode(""); setError(""); }}
            className="w-full text-center text-xs text-muted-foreground hover:text-foreground"
          >
            {useBackup ? t("desktop.setup.useTotpCode") : t("desktop.setup.useBackupCode")}
          </button>

          <button
            type="button"
            onClick={handleBackToLogin}
            className="w-full text-center text-xs text-muted-foreground hover:text-foreground"
          >
            {t("desktop.setup.backToLogin")}
          </button>
        </form>
      </SetupShell>
    );
  }

  return (
    <SetupShell subtitle={t("desktop.setup.subtitle")} onOpenSettings={onOpenSettings}>
      <form
        onSubmit={handleSubmit}
        className="w-full max-w-sm space-y-4 rounded-xl bg-card p-6 shadow-md"
      >
        <div>
          <label className="mb-1 block text-xs font-medium text-muted-foreground">
            {t("desktop.setup.serverUrl")}
          </label>
          <div className="relative">
            <Server className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground/70" />
            <Input
              type="url"
              required
              value={serverUrl}
              onChange={(e) => setServerUrl(e.target.value)}
              placeholder={t("desktop.setup.serverUrlPlaceholder")}
              className="rounded-lg py-2 pl-9 pr-9 h-auto"
            />
            {checkState === "checking" && (
              <span
                className="absolute right-3 top-1/2 -translate-y-1/2"
                title={t("desktop.setup.serverCheckChecking")}
              >
                <Loader2 className="h-4 w-4 animate-spin text-muted-foreground/70" />
              </span>
            )}
            {checkState === "ok" && (
              <span
                className="absolute right-3 top-1/2 -translate-y-1/2"
                title={t("desktop.setup.serverCheckOk")}
              >
                <CheckCircle2 className="h-4 w-4 text-green-600 dark:text-green-400" />
              </span>
            )}
            {checkState === "invalid" && (
              <span
                className="absolute right-3 top-1/2 -translate-y-1/2"
                title={t("desktop.setup.serverCheckInvalid")}
              >
                <XCircle className="h-4 w-4 text-red-600 dark:text-red-400" />
              </span>
            )}
          </div>
        </div>

        <div>
          <label className="mb-1 block text-xs font-medium text-muted-foreground">
            {t("desktop.setup.username")}
          </label>
          <div className="relative">
            <User className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground/70" />
            <Input
              type="text"
              required
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder={t("desktop.setup.usernamePlaceholder")}
              className="rounded-lg py-2 pl-9 h-auto"
            />
          </div>
        </div>

        <div>
          <label className="mb-1 block text-xs font-medium text-muted-foreground">
            {t("desktop.setup.password")}
          </label>
          <div className="relative">
            <Lock className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground/70" />
            <Input
              type="password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t("desktop.setup.passwordPlaceholder")}
              className="rounded-lg py-2 pl-9 h-auto"
            />
          </div>
        </div>

        {error && (
          <p className="rounded-lg bg-red-50 dark:bg-red-950/40 px-3 py-2 text-xs text-red-700 dark:text-red-300">
            {error}
          </p>
        )}

        <Button type="submit" disabled={loading} className="w-full rounded-lg">
          {loading ? t("desktop.setup.submitting") : t("desktop.setup.submit")}
        </Button>
      </form>
    </SetupShell>
  );
}
