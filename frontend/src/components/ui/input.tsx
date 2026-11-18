import * as React from "react"
import { cn } from "@/lib/utils"

const Input = React.forwardRef<HTMLInputElement, React.ComponentProps<"input">>(
  function Input({ className, type, ...props }, ref) {
    return (
      <input
        ref={ref}
        type={type}
      data-slot="input"
      className={cn(
        "surface-inset h-8 w-full min-w-0 rounded-lg px-2.5 py-1 text-base transition-[background-color,box-shadow] outline-none file:inline-flex file:h-6 file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-muted-foreground hover:bg-control focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring/70 disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:outline-2 aria-invalid:outline-offset-1 aria-invalid:outline-danger/50 md:text-sm",
        className
      )}
      {...props}
      />
    )
  },
)

export { Input }
