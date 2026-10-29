import type { VariantProps } from 'class-variance-authority'
import { cva } from 'class-variance-authority'

export { default as Button } from './Button.vue'

export const buttonVariants = cva(
  'group/button inline-flex shrink-0 items-center justify-center gap-2 whitespace-nowrap rounded-lg border border-transparent bg-clip-padding text-sm font-semibold leading-5 transition-colors outline-none focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring aria-invalid:border-destructive disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*=size-])]:size-4',
  {
    variants: {
      variant: {
        default: 'bg-primary text-primary-foreground hover:bg-primary-hover',
        outline: 'border-input bg-card text-foreground hover:border-primary hover:bg-secondary hover:text-primary',
        secondary: 'bg-secondary text-secondary-foreground hover:bg-accent hover:text-accent-foreground',
        ghost: 'text-foreground hover:bg-secondary hover:text-primary',
        destructive: 'border-destructive/30 bg-danger-muted text-destructive hover:bg-danger-hover focus-visible:outline-destructive',
        link: 'text-primary underline-offset-4 hover:text-primary-hover hover:underline',
      },
      size: {
        default: 'min-h-[var(--control-height)] px-5 py-3',
        xs: 'min-h-[var(--control-height-sm)] px-3 py-2.5 text-xs',
        sm: 'min-h-[var(--control-height-sm)] px-4 py-2.5',
        lg: 'min-h-[var(--control-height-lg)] px-6 py-3.5 text-base',
        icon: 'size-[var(--control-height)] p-0',
        'icon-xs': 'size-[var(--control-height-sm)] p-0',
        'icon-sm': 'size-[var(--control-height-sm)] p-0',
        'icon-lg': 'size-[var(--control-height-lg)] p-0',
      },
    },
    defaultVariants: { variant: 'default', size: 'default' },
  },
)
export type ButtonVariants = VariantProps<typeof buttonVariants>
