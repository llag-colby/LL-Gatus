<template>
  <div class="relative">
    <Button variant="ghost" size="icon" class="h-9 w-9" @click="open = !open"
      data-tooltip="Test & customize" data-tip-pos="bottom">
      <FlaskConical class="h-5 w-5" />
    </Button>

    <!-- click-away backdrop -->
    <div v-if="open" class="fixed inset-0 z-40" @click="open = false"></div>

    <div v-if="open" class="pop-in absolute right-0 mt-2 w-72 rounded-lg border bg-popover text-popover-foreground shadow-xl p-3 z-50 space-y-3.5" style="transform-origin: top right;">

      <!-- Status colors -->
      <div>
        <div class="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-2">Status colors</div>
        <div class="space-y-1.5">
          <div v-for="row in colorRows" :key="row.key" class="flex items-center justify-between gap-3">
            <div class="flex items-center gap-2">
              <span class="inline-block h-3.5 w-3.5 rounded-sm border" :style="{ backgroundColor: statusColors[row.key] }"></span>
              <span class="text-sm">{{ row.label }}</span>
            </div>
            <div class="flex items-center gap-2">
              <span class="font-mono text-[10px] text-muted-foreground uppercase">{{ statusColors[row.key] }}</span>
              <input type="color" :value="statusColors[row.key]" @input="setStatusColor(row.key, $event.target.value)"
                class="h-7 w-9 cursor-pointer rounded border bg-transparent p-0" :aria-label="`${row.label} color`" />
            </div>
          </div>
        </div>
        <button class="mt-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors" @click="resetStatusColors">
          Reset to defaults
        </button>
      </div>

      <div class="border-t"></div>

      <!-- Play a sound -->
      <div>
        <div class="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-2">Play a sound</div>
        <div class="flex gap-1">
          <Button size="sm" variant="outline" class="flex-1" @click="test(playUp)">Up</Button>
          <Button size="sm" variant="outline" class="flex-1" @click="test(playDegraded)">Degraded</Button>
          <Button size="sm" variant="outline" class="flex-1" @click="test(playDown)">Down</Button>
        </div>
      </div>

      <!-- Force a site's status -->
      <div>
        <div class="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-2">Simulate outage</div>
        <select v-model="selected" class="w-full text-sm bg-background border rounded-md px-2 py-1 mb-2 focus:outline-none focus:ring-2 focus:ring-ring">
          <option v-for="n in knownLocations" :key="n" :value="n">{{ n }}</option>
        </select>
        <div class="grid grid-cols-2 gap-1">
          <Button size="sm" variant="outline" @click="set('unhealthy')">Down</Button>
          <Button size="sm" variant="outline" @click="set('degraded')">Degraded</Button>
          <Button size="sm" variant="outline" @click="set('healthy')">Up</Button>
          <Button size="sm" variant="outline" @click="set(null)">Clear</Button>
        </div>
      </div>

      <Button size="sm" variant="ghost" class="w-full text-muted-foreground" @click="clearSimulations">
        Clear all simulations
      </Button>
    </div>
  </div>
</template>

<script setup>
import { ref, watch } from 'vue'
import { FlaskConical } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { knownLocations, setSimulation, clearSimulations, statusColors, setStatusColor, resetStatusColors } from '@/store'
import { playUp, playDown, playDegraded, unlockAudio } from '@/utils/sounds'

const open = ref(false)
const selected = ref('')

const colorRows = [
  { key: 'up', label: 'Up / Healthy' },
  { key: 'degraded', label: 'Degraded' },
  { key: 'down', label: 'Down' },
]

// Default the selector to the first location once we know them.
watch(knownLocations, (list) => {
  if (!selected.value && list && list.length) selected.value = list[0]
}, { immediate: true })

const test = (fn) => { unlockAudio(); fn() }
const set = (status) => { unlockAudio(); setSimulation(selected.value, status) }
</script>
