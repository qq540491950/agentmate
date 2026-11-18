import * as React from "react"
import { cn } from "@/lib/utils"

const Textarea = React.forwardRef<HTMLTextAreaElement, React.ComponentProps<"textarea">>(
  function Textarea({ className, ...props }, ref) {
    return (
      <textarea
        ref={ref}
        data-slot="textarea"
        className={cn(
          "surface-inset flex field-sizing-content min-h-16 w-full rounded-lg px-2.5 py-2 text-base transition-[background-color,box-shadow] outline-none placeholder:text-muted-foreground hover:bg-control focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring/70 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:outline-2 aria-invalid:outline-offset-1 aria-invalid:outline-danger/50 md:text-sm",
          className
        )}
        {...props}
      />
    )
  },
)

export { Textarea }
