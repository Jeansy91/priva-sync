import type { ReactNode } from "react";

interface SettingsCardProps {
  title:        string;
  description?: string;
  icon:         ReactNode;
  children:     ReactNode;
}

/**
 * One settings group rendered as a card with a leading icon chip, title and
 * optional description in the header — mirrors the web app's SectionCard look
 * (apps/web SettingsPrimitives), scaled down for the compact desktop panel.
 */
export function SettingsCard({ title, description, icon, children }: SettingsCardProps) {
  return (
    <div className="rounded-xl border border-border bg-card p-4 shadow-xs space-y-3">
      <div className="flex items-start gap-3">
        <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg
                        bg-muted/40 text-muted-foreground/70">
          {icon}
        </div>
        <div className="min-w-0">
          <h3 className="text-sm font-semibold leading-tight text-foreground">{title}</h3>
          {description && (
            <p className="mt-0.5 text-xs text-muted-foreground/70">{description}</p>
          )}
        </div>
      </div>
      {children}
    </div>
  );
}
