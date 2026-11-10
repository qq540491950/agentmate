import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/**
 * Shared shell for the management pages (Settings tabs). One scroll container,
 * one max width and one padding scale so every tab reads with the same rhythm;
 * PageHeader carries the 20px title / 13px description / right-aligned actions
 * pattern that used to be re-implemented per page at 18px.
 */

export function PageShell({ className, children }: {
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className="h-full overflow-y-auto">
      <div className={cn("mx-auto w-full max-w-5xl px-8 py-7", className)}>{children}</div>
    </div>
  );
}

export function PageHeader({ title, description, actions }: {
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-1 text-ui text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  );
}

export function Section({ title, description, actions, className, children }: {
  title: string;
  description?: string;
  actions?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section className={cn("grid gap-3", className)}>
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div className="min-w-0">
          <h2 className="text-base font-semibold">{title}</h2>
          {description && <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>}
        </div>
        {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
      </div>
      {children}
    </section>
  );
}

/** SettingRow is the label-left / control-right row used across settings.
 *  It uses spacing and a hover surface, never a persistent divider. */
export function SettingRow({ label, description, children, className }: {
  label: string;
  description?: string;
  children?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex items-center justify-between gap-4 rounded-lg px-3.5 py-3 transition-colors hover:bg-accent/60", className)}>
      <div className="min-w-0">
        <div className="text-sm font-medium">{label}</div>
        {description && <div className="mt-0.5 text-xs leading-relaxed text-muted-foreground">{description}</div>}
      </div>
      {children && <div className="shrink-0">{children}</div>}
    </div>
  );
}
