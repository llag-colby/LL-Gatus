<template>
  <div class="flex flex-wrap items-center gap-2 sm:gap-3">
    <!-- Search -->
    <div class="relative" data-tooltip="Search by location or connection" data-tip-pos="bottom">
      <Search class="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground pointer-events-none" />
      <label for="search-input" class="sr-only">Search locations</label>
      <Input
        id="search-input"
        v-model="controls.searchQuery"
        type="text"
        placeholder="Search locations..."
        class="pl-9 h-9 w-full sm:w-52 lg:w-64 text-sm"
      />
    </div>

    <!-- View: stacked (vertical) vs grid (horizontal) -->
    <div class="inline-flex items-center rounded-md border bg-background p-0.5" role="group" aria-label="Dashboard view">
      <button
        type="button"
        class="view-btn"
        :class="{ active: dashboardView === 'vertical' }"
        @click="setDashboardView('vertical')"
        data-tooltip="Vertical — one location per row"
        data-tip-pos="bottom"
        aria-label="Vertical view"
      >
        <List class="h-4 w-4" />
      </button>
      <button
        type="button"
        class="view-btn"
        :class="{ active: dashboardView === 'horizontal' }"
        @click="setDashboardView('horizontal')"
        data-tooltip="Horizontal — grid of locations"
        data-tip-pos="bottom"
        aria-label="Horizontal view"
      >
        <LayoutGrid class="h-4 w-4" />
      </button>
    </div>
  </div>
</template>

<script setup>
import { Search, List, LayoutGrid } from 'lucide-vue-next'
import { Input } from '@/components/ui/input'
import { controls, dashboardView, setDashboardView } from '@/store'
</script>

<style scoped>
.view-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 1.75rem;
  width: 2rem;
  border-radius: 0.3rem;
  color: hsl(var(--muted-foreground));
  transition: background 0.15s ease, color 0.15s ease;
}
.view-btn:hover {
  color: hsl(var(--foreground));
  background: hsl(var(--accent) / 0.5);
}
.view-btn.active {
  color: hsl(var(--accent-foreground));
  background: hsl(var(--accent));
}
.view-btn:focus-visible {
  outline: 2px solid hsl(var(--ring));
  outline-offset: 1px;
}
</style>
