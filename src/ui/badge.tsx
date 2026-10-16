import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"
import { Slot } from "radix-ui"

const badgeVariants = cva(
  "inline-flex w-fit shrink-0 max-w-full items-center justify-center gap-1 overflow-hidden rounded-full border border-transparent px-2 py-0.5 text-xs font-medium focus-visible:outline-2 focus-visible:outline-ring focus-visible:-outline-offset-2 aria-invalid:border-destructive aria-invalid:ring-destructive/20 [&>svg]:pointer-events-none [&>svg]:size-3",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground [a&]:hover:bg-primary-hover",
        secondary:
          "bg-secondary text-secondary-foreground [a&]:hover:bg-secondary-hover",
        destructive:
          "bg-destructive text-destructive-foreground [a&]:hover:bg-danger",
        outline:
          "border-border text-foreground [a&]:hover:bg-accent [a&]:hover:text-accent-foreground",
        ghost: "[a&]:hover:bg-accent [a&]:hover:text-accent-foreground",
        link: "text-primary-fg underline-offset-4 [a&]:hover:underline",
        // Kowa's status labels: GitHub's tinted label surface in the state's
        // own colour, so a machine state stays legible next to its label.
        success: "border-success-border bg-success-muted text-success",
        attention: "border-attention-border bg-attention-muted text-attention",
        danger: "border-danger-border bg-danger-muted text-danger",
        done: "border-done-border bg-done-muted text-done",
        neutral: "border-neutral-border bg-neutral-muted text-neutral",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

function Badge({
  className,
  variant = "default",
  asChild = false,
  ...props
}: React.ComponentProps<"span"> &
  VariantProps<typeof badgeVariants> & { asChild?: boolean }) {
  const Comp = asChild ? Slot.Root : "span"

  return (
    <Comp
      data-slot="badge"
      data-variant={variant}
      className={cn(badgeVariants({ variant }), className)}
      {...props}
    />
  )
}

export { Badge, badgeVariants }
