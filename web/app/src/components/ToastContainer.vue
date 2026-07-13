<template>
  <div class="toast-wrap" aria-live="polite" aria-atomic="true">
    <transition-group name="toast">
      <div v-for="t in toasts" :key="t.id" class="toast" :class="'toast-' + t.type" @click="removeToast(t.id)">
        <component :is="iconFor(t.type)" class="h-4 w-4 shrink-0" />
        <span class="leading-snug">{{ t.message }}</span>
      </div>
    </transition-group>
  </div>
</template>

<script setup>
import { CheckCircle2, AlertTriangle, Info } from 'lucide-vue-next'
import { toasts, removeToast } from '@/store'

const iconFor = (type) => (type === 'success' ? CheckCircle2 : type === 'error' ? AlertTriangle : Info)
</script>

<style scoped>
.toast-wrap {
  position: fixed; bottom: 1rem; right: 1rem; z-index: 100;
  display: flex; flex-direction: column; gap: 0.5rem; pointer-events: none;
}
.toast {
  display: flex; align-items: center; gap: 0.5rem;
  min-width: 220px; max-width: 360px; padding: 0.6rem 0.8rem;
  border-radius: 8px; font-size: 0.85rem; cursor: pointer; pointer-events: auto;
  color: hsl(var(--card-foreground)); background: hsl(var(--card));
  border: 1px solid hsl(var(--border));
  box-shadow: 0 10px 30px -8px rgb(0 0 0 / .4), 0 3px 8px -3px rgb(0 0 0 / .3);
}
.toast-success { border-left: 3px solid var(--status-up); }
.toast-error   { border-left: 3px solid var(--status-down); }
.toast-info    { border-left: 3px solid hsl(var(--muted-foreground)); }

.toast-enter-active { transition: opacity var(--dur-3, .26s) var(--ease-out-quart), transform var(--dur-3, .26s) var(--ease-out-quart); }
.toast-leave-active { transition: opacity var(--dur-2, .18s) ease, transform var(--dur-2, .18s) ease; position: absolute; right: 0; }
.toast-enter-from, .toast-leave-to { opacity: 0; transform: translateX(16px) scale(0.96); }
/* When one toast dismisses, the rest glide into their new positions. */
.toast-move { transition: transform var(--dur-3, .26s) var(--ease-out-quart); }

@media (prefers-reduced-motion: reduce) {
  .toast-enter-active, .toast-leave-active, .toast-move { transition: none; }
}
</style>
