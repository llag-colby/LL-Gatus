<template>
  <div v-if="authState.enabled" class="um">
    <!-- Signed out: one button, straight into the dialog. -->
    <Button
      v-if="!authState.authenticated"
      variant="ghost"
      size="sm"
      class="h-9 px-3 text-sm"
      @click="loginOpen = true"
    >
      <LogIn class="mr-2 h-4 w-4" />
      Sign in
    </Button>

    <!-- Signed in: name, role, and a menu. -->
    <button
      v-else
      ref="triggerEl"
      type="button"
      class="um-trigger"
      :class="{ open }"
      aria-haspopup="menu"
      :aria-expanded="open ? 'true' : 'false'"
      :aria-label="`Account menu for ${username}`"
      @click="toggleOpen"
    >
      <User class="um-ico" aria-hidden="true" />
      <span class="um-name">{{ username }}</span>
      <span class="um-role">{{ role }}</span>
    </button>

    <!-- Teleported and positioned in viewport coordinates, matching
         CardSettingsMenu: the sticky header clips, and a fixed panel parented
         elsewhere can never be cropped by it. -->
    <Teleport to="body">
      <div
        v-if="open"
        ref="panelEl"
        class="um-pop"
        :role="showPassword ? 'dialog' : 'menu'"
        :aria-label="showPassword ? 'Change password' : 'Account menu'"
        tabindex="-1"
        :style="{ top: `${top}px`, left: `${left}px` }"
        @keydown.esc.stop.prevent="close"
        @keydown.tab="wrapFocus"
      >
        <div class="pop-head">
          <div class="eyebrow">Signed in as</div>
          <div class="pop-title">{{ username }}</div>
          <div class="pop-sub">{{ roleDescription }}</div>
        </div>

        <ul v-if="!showPassword" class="pop-rows">
          <li>
            <router-link to="/settings" class="pop-item" role="menuitem" @click="close">
              <SlidersHorizontal class="item-ico" aria-hidden="true" />
              Settings
            </router-link>
          </li>
          <li>
            <button type="button" class="pop-item" role="menuitem" @click="openPassword">
              <KeyRound class="item-ico" aria-hidden="true" />
              Change password
            </button>
          </li>
          <li>
            <button type="button" class="pop-item" role="menuitem" :disabled="busy" @click="signOut">
              <LogOut class="item-ico" aria-hidden="true" />
              Sign out
            </button>
          </li>
        </ul>

        <form v-else class="pop-form" @submit.prevent="submitPassword">
          <label class="pw-field">
            <span class="pw-label">Current password</span>
            <input
              ref="currentEl"
              v-model="currentPassword"
              type="password"
              autocomplete="current-password"
              class="pw-input"
              :disabled="busy"
              required
            />
          </label>
          <label class="pw-field">
            <span class="pw-label">New password</span>
            <input
              v-model="newPassword"
              type="password"
              autocomplete="new-password"
              class="pw-input"
              :disabled="busy"
              required
            />
          </label>

          <p v-if="passwordError" class="pw-error" role="alert">{{ passwordError }}</p>

          <div class="pw-actions">
            <Button type="button" variant="outline" size="sm" class="h-8 text-xs" :disabled="busy" @click="cancelPassword">
              Cancel
            </Button>
            <Button type="submit" size="sm" class="h-8 text-xs" :disabled="busy">
              {{ busy ? 'Saving' : 'Save password' }}
            </Button>
          </div>
        </form>
      </div>
    </Teleport>

    <LoginDialog :open="loginOpen" @close="loginOpen = false" />
  </div>
</template>

<script setup>
import { computed, nextTick, onUnmounted, ref } from 'vue'
import { KeyRound, LogIn, LogOut, SlidersHorizontal, User } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import LoginDialog from './LoginDialog.vue'
import { addToast, authState, changePassword, currentRole, logout } from '@/store'

const open = ref(false)
const loginOpen = ref(false)
const busy = ref(false)
const showPassword = ref(false)
const currentPassword = ref('')
const newPassword = ref('')
const passwordError = ref('')

const top = ref(0)
const left = ref(0)
const triggerEl = ref(null)
const panelEl = ref(null)
const currentEl = ref(null)

const MARGIN = 8
const GAP = 6
const FALLBACK_WIDTH = 230

const username = computed(() => (authState.user && authState.user.username) || 'Account')
const role = computed(() => currentRole.value)
const roleDescription = computed(() => {
  if (role.value === 'admin') return 'Admin: full access to settings and monitoring.'
  if (role.value === 'operator') return 'Operator: can pause and resume monitoring.'
  return 'Viewer: read only.'
})

// --- positioning: viewport coordinates, clamped on both axes ---------------
const place = async () => {
  const trigger = triggerEl.value
  if (!trigger) return
  await nextTick()
  const rect = trigger.getBoundingClientRect()
  const panel = panelEl.value
  const width = panel ? panel.offsetWidth || FALLBACK_WIDTH : FALLBACK_WIDTH
  const height = panel ? panel.offsetHeight || 0 : 0

  let nextTop = rect.bottom + GAP
  if (height && nextTop + height > window.innerHeight - MARGIN) {
    const above = rect.top - height - GAP
    nextTop = above >= MARGIN ? above : Math.max(MARGIN, window.innerHeight - height - MARGIN)
  }
  let nextLeft = rect.right - width

  nextLeft = Math.max(MARGIN, Math.min(nextLeft, window.innerWidth - width - MARGIN))
  nextTop = Math.max(MARGIN, Math.min(nextTop, window.innerHeight - (height || 0) - MARGIN))

  // Integer coordinates only: half-pixel offsets soften the text.
  left.value = Math.round(nextLeft)
  top.value = Math.round(nextTop)
}

// --- open / close ----------------------------------------------------------
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

const onDocumentMousedown = (event) => {
  if (panelEl.value && panelEl.value.contains(event.target)) return
  if (triggerEl.value && triggerEl.value.contains(event.target)) return
  close()
}

const onDocumentKeydown = (event) => {
  if (event.key === 'Escape') close()
}

// Stay anchored while things move; give up and close once the trigger has
// scrolled out of view, so the panel never floats free of what it belongs to.
const onViewportChange = () => {
  const rect = triggerEl.value && triggerEl.value.getBoundingClientRect()
  if (!rect || rect.bottom < 0 || rect.top > window.innerHeight || rect.right < 0 || rect.left > window.innerWidth) {
    close()
    return
  }
  place()
}

const addListeners = () => {
  document.addEventListener('mousedown', onDocumentMousedown)
  document.addEventListener('keydown', onDocumentKeydown)
  window.addEventListener('resize', onViewportChange)
  window.addEventListener('scroll', onViewportChange, true)
}

const removeListeners = () => {
  document.removeEventListener('mousedown', onDocumentMousedown)
  document.removeEventListener('keydown', onDocumentKeydown)
  window.removeEventListener('resize', onViewportChange)
  window.removeEventListener('scroll', onViewportChange, true)
}

const openMenu = async () => {
  open.value = true
  showPassword.value = false
  addListeners()
  await place()
  // Measure again now that the panel has real dimensions, then take focus.
  await place()
  if (panelEl.value) panelEl.value.focus()
}

function close() {
  if (!open.value || busy.value) return
  open.value = false
  showPassword.value = false
  currentPassword.value = ''
  newPassword.value = ''
  passwordError.value = ''
  removeListeners()
  if (triggerEl.value) triggerEl.value.focus()
}

const toggleOpen = () => {
  if (open.value) close()
  else openMenu()
}

// --- change password -------------------------------------------------------
const openPassword = async () => {
  passwordError.value = ''
  currentPassword.value = ''
  newPassword.value = ''
  showPassword.value = true
  await place()
  await place()
  if (currentEl.value) currentEl.value.focus()
}

const cancelPassword = async () => {
  showPassword.value = false
  currentPassword.value = ''
  newPassword.value = ''
  passwordError.value = ''
  await place()
  if (panelEl.value) panelEl.value.focus()
}

const submitPassword = async () => {
  if (busy.value) return
  passwordError.value = ''
  busy.value = true
  try {
    await changePassword(currentPassword.value, newPassword.value)
    currentPassword.value = ''
    newPassword.value = ''
    busy.value = false
    close()
    addToast('Password changed', 'success')
  } catch (e) {
    passwordError.value = (e && e.message) || 'Could not change the password. Try again.'
    currentPassword.value = ''
    newPassword.value = ''
    busy.value = false
    await place()
  }
}

// --- sign out --------------------------------------------------------------
const signOut = async () => {
  if (busy.value) return
  busy.value = true
  try {
    await logout()
    busy.value = false
    close()
    addToast('Signed out', 'info')
  } catch (e) {
    busy.value = false
    passwordError.value = ''
    addToast('Could not sign out. Try again.', 'error')
  }
}

onUnmounted(removeListeners)
</script>

<style scoped>
.um { display: inline-flex; }

/* --- trigger --- */
.um-trigger {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  height: 36px;
  max-width: 200px;
  padding: 0 0.6rem;
  border-radius: 8px;
  border: 1px solid transparent;
  background: transparent;
  color: hsl(var(--muted-foreground));
  font-size: 0.8rem;
  cursor: pointer;
}
.um-trigger:hover { color: hsl(var(--foreground)); background: hsl(var(--accent) / 0.6); }
.um-trigger.open { color: hsl(var(--foreground)); background: hsl(var(--accent) / 0.6); border-color: hsl(var(--border)); }
.um-trigger:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 1px; }
.um-ico { width: 16px; height: 16px; flex-shrink: 0; }
.um-name {
  max-width: 110px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 600;
}
.um-role {
  flex-shrink: 0;
  padding: 0.05rem 0.35rem;
  border: 1px solid hsl(var(--border));
  border-radius: 999px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  font-weight: 700;
}
@media (prefers-reduced-motion: no-preference) {
  .um-trigger { transition: color 0.14s ease, background 0.14s ease; }
}
@media (max-width: 640px) {
  .um-name { display: none; }
}

/* --- popover --- */
.um-pop {
  position: fixed;
  z-index: 60;
  width: 230px;
  max-width: calc(100vw - 16px);
  background: hsl(var(--card));
  color: hsl(var(--foreground));
  border: 1px solid hsl(var(--border));
  border-radius: 12px;
  box-shadow: 0 10px 30px -8px rgb(0 0 0 / 0.45);
  padding: 0.7rem 0.75rem 0.65rem;
  text-align: left;
}
.um-pop:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; }

.eyebrow {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  font-weight: 700;
  color: hsl(var(--muted-foreground));
}
.pop-title {
  font-size: 0.86rem;
  font-weight: 700;
  line-height: 1.2;
  margin-top: 0.1rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pop-sub {
  margin-top: 0.2rem;
  font-size: 0.7rem;
  line-height: 1.35;
  color: hsl(var(--muted-foreground));
}

/* --- menu rows --- */
.pop-rows {
  list-style: none;
  margin: 0.6rem 0 0;
  padding: 0.5rem 0 0;
  border-top: 1px solid hsl(var(--border));
}
.pop-item {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  width: 100%;
  padding: 0.35rem 0.4rem;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: hsl(var(--foreground));
  font-size: 0.78rem;
  text-align: left;
  text-decoration: none;
  cursor: pointer;
}
.pop-item:hover { background: hsl(var(--accent) / 0.6); }
.pop-item:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: -2px; }
.pop-item:disabled { opacity: 0.55; cursor: not-allowed; }
.item-ico { width: 14px; height: 14px; flex-shrink: 0; color: hsl(var(--muted-foreground)); }

/* --- change-password form --- */
.pop-form {
  margin-top: 0.6rem;
  padding-top: 0.55rem;
  border-top: 1px solid hsl(var(--border));
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.pw-field { display: flex; flex-direction: column; gap: 0.2rem; }
.pw-label {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  font-weight: 700;
  color: hsl(var(--muted-foreground));
}
.pw-input {
  width: 100%;
  height: 30px;
  padding: 0 0.45rem;
  font-size: 0.78rem;
  color: hsl(var(--foreground));
  background: hsl(var(--background));
  border: 1px solid hsl(var(--border));
  border-radius: 8px;
}
.pw-input:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 1px; }
.pw-input:disabled { opacity: 0.6; cursor: not-allowed; }
.pw-error {
  margin: 0;
  font-size: 0.7rem;
  line-height: 1.35;
  color: hsl(var(--destructive));
}
.pw-actions { display: flex; justify-content: flex-end; gap: 0.4rem; }
</style>
