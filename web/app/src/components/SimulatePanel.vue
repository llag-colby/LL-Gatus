<template>
  <div class="relative">
    <Button variant="ghost" size="icon" class="h-9 w-9" @click="open = !open"
      data-tooltip="Simulate outage (test)" data-tip-pos="bottom">
      <FlaskConical class="h-5 w-5" />
    </Button>

    <!-- click-away backdrop -->
    <div v-if="open" class="fixed inset-0 z-40" @click="open = false"></div>

    <div v-if="open" class="absolute right-0 mt-2 w-64 rounded-lg border bg-popover text-popover-foreground shadow-xl p-3 z-50 space-y-3">
      <div class="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Simulate / Test</div>

      <div>
        <div class="text-xs text-muted-foreground mb-1">Play a sound</div>
        <div class="flex gap-1">
          <Button size="sm" variant="outline" class="flex-1" @click="test(playUp)">Up</Button>
          <Button size="sm" variant="outline" class="flex-1" @click="test(playDegraded)">Degraded</Button>
          <Button size="sm" variant="outline" class="flex-1" @click="test(playDown)">Down</Button>
        </div>
      </div>

      <div>
        <div class="text-xs text-muted-foreground mb-1">Force a site's status</div>
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
import { knownLocations, setSimulation, clearSimulations } from '@/store'
import { playUp, playDown, playDegraded, unlockAudio } from '@/utils/sounds'

const open = ref(false)
const selected = ref('')

// Default the selector to the first location once we know them.
watch(knownLocations, (list) => {
  if (!selected.value && list && list.length) selected.value = list[0]
}, { immediate: true })

const test = (fn) => { unlockAudio(); fn() }
const set = (status) => { unlockAudio(); setSimulation(selected.value, status) }
</script>
