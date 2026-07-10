<template>
  <div :class="combineClasses(badgeVariants({ variant }), $attrs.class ?? '')">
    <slot />
  </div>
</template>

<script setup>
import { cva } from 'class-variance-authority'
import { combineClasses } from '@/utils/misc'

defineProps({
  variant: {
    type: String,
    default: 'default',
  },
})

const badgeVariants = cva(
  'inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2',
  {
    variants: {
      variant: {
        default: 'border-transparent bg-primary text-primary-foreground hover:bg-primary/80',
        secondary: 'border-transparent bg-secondary text-secondary-foreground hover:bg-secondary/80',
        destructive: 'border-transparent st-bg-down text-white',
        outline: 'text-foreground',
        success: 'border-transparent st-bg-up text-white',
        warning: 'border-transparent st-bg-degraded text-white',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  }
)
</script>