<template>
  <!-- Teleported to body so the header's stacking context and overflow can't
       crop or stack anything over the modal. -->
  <Teleport to="body">
    <div v-if="open" class="ld-backdrop" @mousedown.self="close">
      <div
        ref="panelEl"
        class="ld-panel"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="titleId"
        tabindex="-1"
        @keydown.esc.stop.prevent="close"
        @keydown.tab="wrapFocus"
      >
        <div class="ld-head">
          <div class="eyebrow">Account</div>
          <h2 :id="titleId" class="ld-title">Sign in</h2>
          <p class="ld-hint">Sign in to change settings and control monitoring.</p>
        </div>

        <form class="ld-form" @submit.prevent="submit">
          <label class="ld-field">
            <span class="ld-label">Username</span>
            <input
              ref="usernameEl"
              v-model="username"
              type="text"
              name="username"
              autocomplete="username"
              autocapitalize="none"
              spellcheck="false"
              class="ld-input"
              :disabled="busy"
              required
            />
          </label>

          <label class="ld-field">
            <span class="ld-label">Password</span>
            <input
              v-model="password"
              type="password"
              name="password"
              autocomplete="current-password"
              class="ld-input"
              :disabled="busy"
              required
            />
          </label>

          <p v-if="error" class="ld-error" role="alert">{{ error }}</p>

          <div class="ld-actions">
            <Button type="button" variant="outline" size="sm" class="h-8 text-xs" :disabled="busy" @click="close">
              Cancel
            </Button>
            <Button type="submit" size="sm" class="h-8 text-xs" :disabled="busy">
              {{ busy ? 'Signing in' : 'Sign in' }}
            </Button>
          </div>
        </form>
      </div>
    </div>
  </Teleport>
</template>

<script setup>
import { nextTick, onUnmounted, ref, useId, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { login } from '@/store'

const props = defineProps({
  open: { type: Boolean, default: false },
})
const emit = defineEmits(['close', 'signed-in'])

const titleId = useId()
const panelEl = ref(null)
const usernameEl = ref(null)

const username = ref('')
const password = ref('')
const error = ref('')
const busy = ref(false)

// Whatever had focus before the dialog opened, so it can be handed back.
let opener = null

const focusables = () =>
  panelEl.value
    ? Array.from(
        panelEl.value.querySelectorAll(
          'input:not([disabled]), button:not([disabled]), [href], [tabindex]:not([tabindex="-1"])'
        )
      )
    : []

const wrapFocus = (event) => {
  const items = focusables()
  if (items.length === 0) return
  const first = items[0]
  const last = items[items.length - 1]
  const active = document.activeElement
  if (event.shiftKey && (active === first || active === panelEl.value)) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && active === last) {
    event.preventDefault()
    first.focus()
  }
}

// Escape works even when focus has slipped outside the panel.
const onDocumentKeydown = (event) => {
  if (event.key === 'Escape') close()
}

function close() {
  if (busy.value) return
  emit('close')
}

const restoreFocus = () => {
  if (opener && typeof opener.focus === 'function' && document.contains(opener)) opener.focus()
  opener = null
}

watch(
  () => props.open,
  async (isOpen) => {
    if (isOpen) {
      opener = document.activeElement
      username.value = ''
      password.value = ''
      error.value = ''
      busy.value = false
      document.addEventListener('keydown', onDocumentKeydown)
      await nextTick()
      if (usernameEl.value) usernameEl.value.focus()
      else if (panelEl.value) panelEl.value.focus()
    } else {
      document.removeEventListener('keydown', onDocumentKeydown)
      password.value = ''
      restoreFocus()
    }
  },
  { immediate: true }
)

const submit = async () => {
  if (busy.value) return
  error.value = ''
  busy.value = true
  try {
    await login(username.value, password.value)
    password.value = ''
    emit('signed-in')
    emit('close')
  } catch (e) {
    error.value = (e && e.message) || 'Sign-in failed. Check your username and password.'
    password.value = ''
    await nextTick()
  } finally {
    busy.value = false
  }
}

onUnmounted(() => {
  document.removeEventListener('keydown', onDocumentKeydown)
})
</script>

<style scoped>
.ld-backdrop {
  position: fixed;
  inset: 0;
  z-index: 70;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 12vh 1rem 1rem;
  background: rgb(0 0 0 / 0.5);
}

.ld-panel {
  width: 320px;
  max-width: calc(100vw - 2rem);
  background: hsl(var(--card));
  color: hsl(var(--foreground));
  border: 1px solid hsl(var(--border));
  border-radius: 12px;
  box-shadow: 0 10px 30px -8px rgb(0 0 0 / 0.45);
  padding: 0.85rem 0.9rem 0.8rem;
  text-align: left;
}
.ld-panel:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; }

.eyebrow {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  font-weight: 700;
  color: hsl(var(--muted-foreground));
}
.ld-title {
  font-size: 0.95rem;
  font-weight: 700;
  line-height: 1.2;
  margin: 0.1rem 0 0;
}
.ld-hint {
  margin: 0.25rem 0 0;
  font-size: 0.7rem;
  line-height: 1.35;
  color: hsl(var(--muted-foreground));
}

.ld-form {
  margin-top: 0.7rem;
  padding-top: 0.65rem;
  border-top: 1px solid hsl(var(--border));
  display: flex;
  flex-direction: column;
  gap: 0.55rem;
}
.ld-field { display: flex; flex-direction: column; gap: 0.22rem; }
.ld-label {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  font-weight: 700;
  color: hsl(var(--muted-foreground));
}
.ld-input {
  width: 100%;
  height: 32px;
  padding: 0 0.5rem;
  font-size: 0.8rem;
  color: hsl(var(--foreground));
  background: hsl(var(--background));
  border: 1px solid hsl(var(--border));
  border-radius: 8px;
}
.ld-input:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 1px; }
.ld-input:disabled { opacity: 0.6; cursor: not-allowed; }

.ld-error {
  margin: 0;
  font-size: 0.72rem;
  line-height: 1.35;
  color: hsl(var(--destructive));
}

.ld-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.4rem;
  margin-top: 0.15rem;
}
</style>
