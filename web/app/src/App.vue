<template>
  <div id="global" ref="globalEl" :class="['bg-background text-foreground', { 'fs-active': isFullscreen }]">
    <!-- Loading State -->
    <div v-if="!retrievedConfig" class="flex items-center justify-center min-h-screen">
      <Loading size="lg" />
    </div>

    <!-- Main App Container -->
    <div v-else-if="!config || !config.oidc || config.authenticated" class="relative">
      <!-- Header -->
      <header class="sticky top-0 z-40 border-b bg-card/70 backdrop-blur supports-[backdrop-filter]:bg-card/60">
        <div class="w-full px-4 sm:px-6 py-3">
          <div class="flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
            <!-- Logo -->
            <router-link
              to="/"
              class="flex items-center shrink-0 hover:opacity-80 transition-opacity"
              data-tooltip="Back to dashboard"
              data-tip-pos="bottom"
            >
              <div class="h-10 flex items-center">
                <img
                  v-if="logo"
                  :src="logo"
                  alt="Long Lewis"
                  class="h-full w-auto object-contain"
                />
                <img
                  v-else
                  src="./assets/logo.svg"
                  alt="Gatus"
                  class="h-full w-auto object-contain"
                />
              </div>
            </router-link>

            <!-- Controls: search / filter / sort + actions -->
            <div class="flex items-center gap-2 sm:gap-3 flex-wrap justify-end grow">
              <SearchBar />

              <div class="flex items-center gap-1">
                <SimulatePanel />
                <Button
                  variant="ghost"
                  size="icon"
                  class="h-9 w-9"
                  @click="toggleSound"
                  :data-tooltip="soundEnabled ? 'Mute alerts' : 'Enable sound alerts'"
                  data-tip-pos="bottom"
                >
                  <Volume2 v-if="soundEnabled" class="h-5 w-5" />
                  <VolumeX v-else class="h-5 w-5" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  class="h-9 w-9"
                  @click="toggleFullscreen"
                  :data-tooltip="isFullscreen ? 'Exit full screen' : 'Full screen'"
                  data-tip-pos="bottom"
                >
                  <Minimize v-if="isFullscreen" class="h-5 w-5" />
                  <Maximize v-else class="h-5 w-5" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  class="h-9 w-9"
                  @click="refreshData"
                  data-tooltip="Refresh data"
                  data-tip-pos="bottom"
                >
                  <RefreshCw class="h-5 w-5" />
                </Button>
              </div>

              <!-- Optional configured navigation buttons -->
              <nav v-if="buttons && buttons.length" class="hidden md:flex items-center gap-1">
                <a
                  v-for="button in buttons"
                  :key="button.name"
                  :href="button.link"
                  target="_blank"
                  class="px-3 py-2 text-sm font-medium rounded-md hover:bg-accent hover:text-accent-foreground transition-colors"
                >
                  {{ button.name }}
                </a>
              </nav>
            </div>
          </div>
        </div>
      </header>

      <!-- Main Content -->
      <main class="relative">
        <router-view @showTooltip="showTooltip" :announcements="announcements" />
      </main>

      <!-- Footer -->
      <footer class="border-t mt-auto">
        <div class="w-full px-4 sm:px-6 py-6">
          <div class="flex flex-col items-center gap-4">
            <div class="text-sm text-muted-foreground text-center">
              <span v-if="buildVersion" class="opacity-60 font-mono text-xs">build #{{ buildVersion }}</span>
            </div>
            <Social />
          </div>
        </div>
      </footer>
    </div>

    <!-- OIDC Login Screen -->
    <div v-else id="login-container" class="flex items-center justify-center min-h-screen p-4">
      <Card class="w-full max-w-md">
        <CardHeader class="text-center">
          <div v-if="logo" class="flex items-center justify-center gap-4 mb-4">
            <img :src="logo" alt="" class="w-20 h-20 object-contain" />
            <div class="w-px h-12 bg-border"></div>
            <img src="./assets/logo.svg" alt="Gatus" class="w-20 h-20" />
          </div>
          <img v-else src="./assets/logo.svg" alt="Gatus" class="w-20 h-20 mx-auto mb-4" />
          <CardTitle class="text-3xl">{{ header }}</CardTitle>
          <p class="text-muted-foreground mt-2">{{ loginSubtitle }}</p>
        </CardHeader>
        <CardContent>
          <div v-if="route && route.query.error" class="mb-6">
            <div class="p-3 rounded-md bg-destructive/10 border border-destructive/20">
              <p class="text-sm text-destructive text-center">
                <span v-if="route.query.error === 'access_denied'">
                  You do not have access to this status page
                </span>
                <span v-else>{{ route.query.error }}</span>
              </p>
            </div>
          </div>
          
          <a
            :href="`/oidc/login`"
            class="inline-flex items-center justify-center whitespace-nowrap rounded-md text-sm font-medium ring-offset-background transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50 bg-primary text-primary-foreground hover:bg-primary/90 h-11 px-8 w-full"
            @click="isOidcLoading = true"
          >
            <Loading v-if="isOidcLoading" size="xs" />
            <template v-else>
              <LogIn class="mr-2 h-4 w-4" />
              Login with OIDC
            </template>
          </a>
        </CardContent>
      </Card>
    </div>

    <!-- Tooltip -->
    <Tooltip :result="tooltip.result" :event="tooltip.event" :isPersistent="tooltipIsPersistent" />

    <!-- Global toast notifications (bottom-right) -->
    <ToastContainer />
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { LogIn, Maximize, Minimize, RefreshCw, Volume2, VolumeX } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Card, CardHeader, CardTitle, CardContent } from '@/components/ui/card'
import SearchBar from './components/SearchBar.vue'
import SimulatePanel from './components/SimulatePanel.vue'
import ToastContainer from './components/ToastContainer.vue'
import Social from './components/Social.vue'
import Tooltip from './components/Tooltip.vue'
import Loading from './components/Loading.vue'
import { requestRefresh, soundEnabled, setSoundEnabled, applyStatusColors } from '@/store'
import { unlockAudio } from '@/utils/sounds'

const route = useRoute()

// State
const retrievedConfig = ref(false)
const config = ref({ oidc: false, authenticated: true })
const announcements = ref([])
const tooltip = ref({})
const isOidcLoading = ref(false)
const tooltipIsPersistent = ref(false)
let configInterval = null

const refreshData = () => requestRefresh()

// Sound alerts toggle (the click also unlocks browser audio).
const toggleSound = () => {
  unlockAudio()
  setSoundEnabled(!soundEnabled.value)
}

// Build version (git SHA baked into the server) so a deploy can be verified.
const buildVersion = ref('')
const fetchVersion = async () => {
  try {
    const response = await fetch('/api/v1/version', { cache: 'no-store' })
    if (response.ok) {
      const data = await response.json()
      buildVersion.value = data.version
    }
  } catch (e) {
    // non-fatal
  }
}

// Full-screen toggle — fullscreen the app container (not the document root) so
// it becomes its own scroll context (drilled-in detail pages can scroll).
const globalEl = ref(null)
const isFullscreen = ref(false)
const toggleFullscreen = () => {
  if (!document.fullscreenElement) {
    globalEl.value?.requestFullscreen?.()
  } else {
    document.exitFullscreen?.()
  }
}
const handleFullscreenChange = () => {
  isFullscreen.value = !!document.fullscreenElement
}

// Computed properties
const logo = computed(() => {
  return window.config && window.config.logo && window.config.logo !== '{{ .UI.Logo }}' ? window.config.logo : ""
})

const header = computed(() => {
  return window.config && window.config.header && window.config.header !== '{{ .UI.Header }}' ? window.config.header : "Gatus"
})

const buttons = computed(() => {
  return window.config && window.config.buttons ? window.config.buttons : []
})

const loginSubtitle = computed(() => {
  return window.config && window.config.loginSubtitle && window.config.loginSubtitle !== '{{ .UI.LoginSubtitle }}' ? window.config.loginSubtitle : "System Monitoring Dashboard"
})

// Methods
const fetchConfig = async () => {
  try {
    const response = await fetch(`/api/v1/config`, { credentials: 'include' })
    if (response.status === 200) {
      const data = await response.json()
      config.value = data
      announcements.value = data.announcements || []
    }
    retrievedConfig.value = true
  } catch (error) {
    console.error('Failed to fetch config:', error)
    retrievedConfig.value = true
  }
}

const showTooltip = (result, event, action = 'hover') => {
  if (action === 'click') {
    if (!result) {
      // Deselecting
      tooltip.value = {}
      tooltipIsPersistent.value = false
    } else {
      // Selecting new data point
      tooltip.value = { result, event }
      tooltipIsPersistent.value = true
    }
  } else if (action === 'hover') {
    // Only update tooltip on hover if not in persistent mode
    if (!tooltipIsPersistent.value) {
      tooltip.value = { result, event }
    }
  }
}

const handleDocumentClick = (event) => {
  // Close persistent tooltip when clicking outside
  if (tooltipIsPersistent.value) {
    const tooltipElement = document.getElementById('tooltip')
    // Check if click is on a data point bar or inside tooltip
    const clickedDataPoint = event.target.closest('.flex-1.h-6, .flex-1.h-8')

    if (tooltipElement && !tooltipElement.contains(event.target) && !clickedDataPoint) {
      tooltip.value = {}
      tooltipIsPersistent.value = false
      // Emit event to clear selections in child components
      window.dispatchEvent(new CustomEvent('clear-data-point-selection'))
    }
  }
}

// Fetch config on mount and set up interval
onMounted(() => {
  applyStatusColors()   // push saved custom colors into CSS vars before first paint
  fetchConfig()
  fetchVersion()
  // Refresh config every 10 minutes for announcements
  configInterval = setInterval(fetchConfig, 600000)
  // Add click listener for closing persistent tooltips
  document.addEventListener('click', handleDocumentClick)
  // Track full-screen state
  document.addEventListener('fullscreenchange', handleFullscreenChange)
  // Unlock audio on the first user interaction (browsers block autoplay)
  window.addEventListener('pointerdown', unlockAudio, { once: true })
  window.addEventListener('keydown', unlockAudio, { once: true })
})

// Clean up interval on unmount
onUnmounted(() => {
  if (configInterval) {
    clearInterval(configInterval)
    configInterval = null
  }
  // Remove click listener
  document.removeEventListener('click', handleDocumentClick)
  document.removeEventListener('fullscreenchange', handleFullscreenChange)
})
</script>