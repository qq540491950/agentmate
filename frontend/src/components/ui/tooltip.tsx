"use client"

import * as React from "react"
import { cn } from "@/lib/utils"
import { Tooltip as TooltipPrimitive } from "radix-ui"

const tooltipCardClassName = "max-w-[min(24rem,calc(100vw-2rem))] rounded-md bg-overlay px-3 py-2 text-xs leading-relaxed text-popover-foreground shadow-pop"
const TooltipProviderPresence = React.createContext(false)

function TooltipProvider({
  delayDuration = 0,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Provider>) {
  return (
    <TooltipProviderPresence.Provider value={true}>
      <TooltipPrimitive.Provider
        data-slot="tooltip-provider"
        delayDuration={delayDuration}
        {...props}
      />
    </TooltipProviderPresence.Provider>
  )
}

function Tooltip({
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Root>) {
  const root = <TooltipPrimitive.Root data-slot="tooltip" {...props} />
  const hasProvider = React.useContext(TooltipProviderPresence)
  return hasProvider
    ? root
    : (
      <TooltipProvider delayDuration={450} skipDelayDuration={300}>
        {root}
      </TooltipProvider>
    )
}

function TooltipTrigger({
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Trigger>) {
  return <TooltipPrimitive.Trigger data-slot="tooltip-trigger" {...props} />
}

function TooltipContent({
  className,
  sideOffset = 6,
  hideArrow = true,
  children,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Content> & {
  /** The shared hover card is a surface, not a speech bubble. Callers that
   *  need the legacy pointer can opt back in. */
  hideArrow?: boolean;
}) {
  return (
    <TooltipPrimitive.Portal>
      <TooltipPrimitive.Content
        data-slot="tooltip-content"
        sideOffset={sideOffset}
        className={cn(
          "z-50 inline-flex w-fit origin-(--radix-tooltip-content-transform-origin) items-center gap-1.5 [overflow-wrap:anywhere] has-data-[slot=kbd]:pr-1.5 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 **:data-[slot=kbd]:relative **:data-[slot=kbd]:isolate **:data-[slot=kbd]:z-50 **:data-[slot=kbd]:rounded-sm data-[state=delayed-open]:animate-in data-[state=delayed-open]:fade-in-0 data-[state=delayed-open]:zoom-in-95 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
          tooltipCardClassName,
          className
        )}
        {...props}
      >
        {children}
        {!hideArrow && (
          <TooltipPrimitive.Arrow className="z-50 size-2.5 translate-y-[calc(-50%_-_2px)] rotate-45 rounded-[2px] bg-overlay fill-overlay" />
        )}
      </TooltipPrimitive.Content>
    </TooltipPrimitive.Portal>
  )
}

function TooltipHint({
  content,
  children,
  side = "top",
  align = "center",
  sideOffset = 6,
  wrap = false,
  contentClassName,
}: {
  content: React.ReactNode;
  children: React.ReactElement;
  side?: React.ComponentProps<typeof TooltipPrimitive.Content>["side"];
  align?: React.ComponentProps<typeof TooltipPrimitive.Content>["align"];
  sideOffset?: number;
  /** Wraps a trigger component that does not forward refs (or nests another
   *  Radix trigger) in an inline-flex span so anchoring stays reliable. */
  wrap?: boolean;
  contentClassName?: string;
}) {
  if (content === null || content === undefined || content === false || content === "") {
    return children;
  }
  const text = typeof content === "string" ? content : "";
  const detail = text.includes("\n") || text.length > 40;
  const trigger = wrap
    ? <span className="inline-flex min-w-0">{children}</span>
    : children;
  return (
    <Tooltip>
      <TooltipTrigger asChild>{trigger}</TooltipTrigger>
      <TooltipContent
        side={side}
        align={align}
        sideOffset={sideOffset}
        className={cn(
          "flex-col gap-0.5",
          detail ? "items-start text-left" : "items-center text-center",
          contentClassName,
        )}
      >
        {text.includes("\n") ? <span className="whitespace-pre-line">{text}</span> : content}
      </TooltipContent>
    </Tooltip>
  );
}

export { Tooltip, TooltipContent, TooltipHint, TooltipProvider, TooltipTrigger, tooltipCardClassName }
