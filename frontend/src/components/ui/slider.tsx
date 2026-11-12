import * as React from "react"
import { Slider as SliderPrimitive } from "radix-ui"
import { cn } from "@/lib/utils"

type SliderProps = React.ComponentProps<typeof SliderPrimitive.Root> & {
  dragging?: boolean
  thumbAriaLabel?: string
  thumbAriaValueText?: string
}

function Slider({
  className,
  dragging,
  thumbAriaLabel,
  thumbAriaValueText,
  ...props
}: SliderProps) {
  return (
    <SliderPrimitive.Root
      data-slot="slider"
      data-dragging={dragging ? "" : undefined}
      className={cn(
        "relative flex w-full touch-none items-center select-none data-[disabled]:opacity-50",
        className,
      )}
      {...props}
    >
      <SliderPrimitive.Track
        data-slot="slider-track"
        className="relative h-6 w-full grow overflow-hidden rounded-full bg-control"
      >
        <SliderPrimitive.Range
          data-slot="slider-range"
          className="absolute h-full bg-brand"
        />
      </SliderPrimitive.Track>
      <SliderPrimitive.Thumb
        data-slot="slider-thumb"
        aria-label={thumbAriaLabel}
        aria-valuetext={thumbAriaValueText}
        className="block size-8 rounded-full bg-primary shadow-pop transition-[box-shadow,transform] focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring/60 disabled:pointer-events-none motion-reduce:transition-none"
      />
    </SliderPrimitive.Root>
  )
}

export { Slider }
