<template>
  <div class="dashboard-container detail-page bg-background">
    <div class="w-full px-4 sm:px-6 py-4 settings-panel">

      <!-- MASTHEAD. Not a title over a form: a panel plate. Who you are on the
           left, the three numbers that describe this system on the right. It
           gives the page a hard top edge and a focal point, which is exactly
           what a stack of equal boxes was missing. -->
      <header class="plate" :class="{ 'is-in': true }">
        <div class="plate-left">
          <router-link to="/" class="back" data-tooltip="Back to dashboard" data-tip-pos="bottom">
            <ArrowLeft class="h-4 w-4" />
          </router-link>
          <div class="plate-id">
            <div class="tick">Access control</div>
            <h1 class="plate-h1">Settings</h1>
          </div>
        </div>

        <div class="plate-right">
          <div v-if="signedIn" class="readout">
            <span class="readout-k">Signed in</span>
            <span class="readout-v mono">{{ me.username }}</span>
            <span class="readout-sub">
              <span class="chip" :class="'chip-' + me.role">{{ me.role }}</span>
              <span class="readout-dim">{{ formatWhen(myLastLogin) }}</span>
            </span>
          </div>
          <div v-else class="readout">
            <span class="readout-k">Session</span>
            <span class="readout-v">Anonymous</span>
            <span class="readout-sub"><span class="readout-dim">read only</span></span>
          </div>

          <div v-if="showAdmin" class="readout">
            <span class="readout-k">Accounts</span>
            <span class="readout-v num">{{ users.length }}</span>
            <span class="readout-sub"><span class="readout-dim">{{ adminCount }} admin</span></span>
          </div>

          <div v-if="showAdmin" class="readout" :class="{ hot: pausedKeys.length > 0 }">
            <span class="readout-k">Paused</span>
            <span class="readout-v num">{{ pausedKeys.length }}</span>
            <span class="readout-sub"><span class="readout-dim">checks stopped</span></span>
          </div>
        </div>
      </header>

      <!-- Accounts are not configured on this deployment -->
      <div v-if="!authState.enabled" class="notice">
        <div class="notice-title">Accounts are not configured</div>
        <p class="notice-body">
          This deployment runs without sign-in, so every viewer has full control and there
          are no users to manage. Configure accounts on the server to enable roles.
        </p>
      </div>

      <div class="body" :class="{ solo: !showAdmin }">

        <!-- LEFT RAIL: who you are, and what that lets you do. -->
        <div class="rail">

          <section v-if="signedIn" class="block">
            <div class="block-head">
              <span class="tick">Your account</span>
            </div>
            <form class="stack" @submit.prevent="submitPassword">
              <div class="fields">
                <label class="lbl">
                  <span>Current password</span>
                  <input v-model="pw.current" class="field" type="password" autocomplete="current-password"
                    :disabled="pwBusy" />
                </label>
                <label class="lbl">
                  <span>New password</span>
                  <input v-model="pw.next" class="field" type="password" autocomplete="new-password"
                    :disabled="pwBusy" />
                </label>
                <label class="lbl">
                  <span>Confirm new password</span>
                  <input v-model="pw.confirm" class="field" type="password" autocomplete="new-password"
                    :disabled="pwBusy" />
                </label>
              </div>
              <div class="row-actions-left">
                <button type="submit" class="btn btn-primary" :disabled="pwBusy">
                  {{ pwBusy ? 'Saving' : 'Change password' }}
                </button>
                <span v-if="pwDone" class="msg msg-ok">Password changed.</span>
              </div>
              <p v-if="pwError" class="msg msg-error" role="alert">{{ pwError }}</p>
            </form>
          </section>

          <div v-else-if="authState.enabled" class="notice">
            <div class="notice-title">Sign in to change settings</div>
            <p class="notice-body">
              Settings are tied to your account. Sign in to change your password, and sign in as
              an admin to manage users and review what is paused.
            </p>
          </div>

          <!-- THE SIGNATURE: the clearance matrix, read as a patch bay. Role
               columns are channels; a lit dot is a closed circuit. Your own
               channel is illuminated, so reading DOWN answers "what can I do"
               and reading ACROSS answers "what would a promotion give me".
               A grid of the words Yes and No answered neither. -->
          <section class="block">
            <div class="block-head">
              <span class="tick">Clearance</span>
              <span v-if="signedIn" class="head-note">your channel is lit</span>
            </div>

            <div class="bay">
              <div class="bay-row bay-head">
                <span class="bay-label"></span>
                <span v-for="r in ROLES" :key="r" class="bay-ch" :class="{ mine: signedIn && me.role === r }">
                  {{ r }}
                </span>
              </div>
              <div v-for="row in PERMISSIONS" :key="row.action" class="bay-row">
                <span class="bay-label">{{ row.action }}</span>
                <span v-for="r in ROLES" :key="r" class="bay-ch"
                  :class="{ mine: signedIn && me.role === r, on: row.roles.includes(r) }">
                  <i class="jack" aria-hidden="true"></i>
                  <span class="sr-only">{{ row.roles.includes(r) ? 'allowed' : 'not allowed' }}</span>
                </span>
              </div>
            </div>
            <p class="block-note">Roles are cumulative: each can do everything to its left.</p>
          </section>
        </div>

        <!-- MAIN: the work. Admin only. -->
        <div v-if="showAdmin" class="main">

          <section class="block">
            <div class="block-head">
              <span class="tick">Roster</span>
              <span class="head-note">{{ users.length }} {{ users.length === 1 ? 'account' : 'accounts' }}</span>
            </div>

            <p v-if="usersError" class="msg msg-error" role="alert">{{ usersError }}</p>
            <p v-else-if="usersLoading && users.length === 0" class="msg msg-dim">Loading accounts.</p>

            <ul v-if="users.length" class="roster">
              <li v-for="u in users" :key="u.id" class="crew" :class="{ armed: confirmId === u.id }">
                <span class="crew-mark" aria-hidden="true">{{ (u.username || '?').slice(0, 2).toUpperCase() }}</span>

                <span class="crew-id">
                  <span class="crew-name mono">
                    {{ u.username }}<span v-if="isMe(u)" class="crew-you">you</span>
                  </span>
                  <span class="crew-seen">seen {{ formatWhen(u.lastLoginAt) }} · added {{ formatWhen(u.createdAt) }}</span>
                </span>

                <select class="field field-sm crew-role" :value="u.role"
                  :disabled="roleLocked(u) || busyId === u.id"
                  :aria-label="`Role for ${u.username}`"
                  @change="onRoleChange(u, $event.target.value)">
                  <option v-for="r in ROLES" :key="r" :value="r">{{ r }}</option>
                </select>

                <span class="crew-do">
                  <button type="button" class="btn btn-quiet" :disabled="busyId === u.id"
                    @click="openReset(u)">Reset password</button>
                  <template v-if="confirmId === u.id">
                    <button type="button" class="btn btn-danger" :disabled="busyId === u.id"
                      @click="deleteUser(u)">Confirm delete</button>
                    <button type="button" class="btn btn-quiet" @click="confirmId = null">Cancel</button>
                  </template>
                  <button v-else type="button" class="btn btn-quiet"
                    :disabled="deleteLocked(u) || busyId === u.id" @click="startDelete(u)">Delete</button>
                </span>

                <div v-if="resetId === u.id || rowErrors[u.id] || rowNote(u)" class="crew-drawer">
                  <form v-if="resetId === u.id" class="reset-form" @submit.prevent="submitReset(u)">
                    <label class="lbl lbl-inline">
                      <span>New password for {{ u.username }}</span>
                      <input v-model="resetValue" class="field field-sm" type="password"
                        autocomplete="new-password" :disabled="busyId === u.id" />
                    </label>
                    <button type="submit" class="btn btn-primary" :disabled="busyId === u.id">Save password</button>
                    <button type="button" class="btn btn-quiet" @click="cancelReset">Cancel</button>
                  </form>
                  <p v-if="rowErrors[u.id]" class="msg msg-error" role="alert">{{ rowErrors[u.id] }}</p>
                  <p v-else-if="rowNote(u)" class="msg msg-dim">{{ rowNote(u) }}</p>
                </div>
              </li>
            </ul>

            <form class="stack add" @submit.prevent="addUser">
              <div class="tick tick-sm">Add an account</div>
              <div class="fields fields-3">
                <label class="lbl">
                  <span>Username</span>
                  <input v-model="draft.username" class="field" type="text" autocomplete="off" :disabled="addBusy" />
                </label>
                <label class="lbl">
                  <span>Password</span>
                  <input v-model="draft.password" class="field" type="password" autocomplete="new-password"
                    :disabled="addBusy" />
                </label>
                <label class="lbl">
                  <span>Role</span>
                  <select v-model="draft.role" class="field" :disabled="addBusy">
                    <option v-for="r in ROLES" :key="r" :value="r">{{ r }}</option>
                  </select>
                </label>
              </div>
              <div class="row-actions-left">
                <button type="submit" class="btn btn-primary" :disabled="addBusy">
                  {{ addBusy ? 'Adding' : 'Add account' }}
                </button>
                <span class="hint">At least one admin must remain, so the last admin cannot be removed.</span>
              </div>
              <p v-if="addError" class="msg msg-error" role="alert">{{ addError }}</p>
            </form>
          </section>

          <section class="block">
            <div class="block-head">
              <span class="tick">Paused checks</span>
              <span class="head-note">global</span>
            </div>

            <p v-if="monitoringError" class="msg msg-error" role="alert">{{ monitoringError }}</p>
            <p v-else-if="monitoringLoading && pausedKeys.length === 0" class="msg msg-dim">Loading paused checks.</p>
            <p v-else-if="pausedKeys.length === 0" class="msg msg-dim">
              Nothing is paused. Every configured check is running.
            </p>
            <ul v-else class="keys">
              <li v-for="key in pausedKeys" :key="key">
                <router-link class="key" :to="`/endpoints/${key}`">
                  <i class="key-led" aria-hidden="true"></i><span class="mono">{{ key }}</span>
                </router-link>
              </li>
            </ul>
            <p class="block-note">
              Pausing stops the check and its alerts for every viewer and for the wallboards.
              Recorded history is kept. Resume from the card or the drill-in.
            </p>
          </section>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { ArrowLeft } from 'lucide-vue-next'
import { addToast, authState, can, changePassword, refreshAuth } from '@/store'

const ROLES = ['viewer', 'operator', 'admin']

// Static reference: kept next to the page it documents so the model stays
// discoverable without reading the server config.
const PERMISSIONS = [
  { action: 'View dashboards', roles: ['viewer', 'operator', 'admin'] },
  { action: 'Force ping and sweep', roles: ['operator', 'admin'] },
  { action: 'Pause monitoring', roles: ['operator', 'admin'] },
  { action: 'Edit thresholds', roles: ['operator', 'admin'] },
  { action: 'Manage users', roles: ['admin'] },
  { action: 'Settings: users and monitoring', roles: ['admin'] },
]

const me = computed(() => authState.user || { id: null, username: 'unknown', role: 'viewer' })
const signedIn = computed(() => !!authState.enabled && !!authState.authenticated)
const showAdmin = computed(() => signedIn.value && can('admin'))

const isMe = (u) => !!u && me.value.id != null && u.id === me.value.id

// --- shared helpers --------------------------------------------------------

const readBody = async (response) => {
  try {
    return await response.json()
  } catch (e) {
    return null
  }
}

// The server sends {"error":"..."}. Show that text as written: on 409 it names
// the last-admin rule, which is far more useful than a status code.
const serverMessage = (body, response, fallback) => {
  if (body && typeof body.error === 'string' && body.error.trim()) return body.error
  return `${fallback} (${response.status})`
}

const thrownMessage = (e, fallback) => (e && e.message ? e.message : fallback)

const formatWhen = (value) => {
  if (!value) return 'Never'
  const d = new Date(value)
  if (isNaN(d.getTime()) || d.getFullYear() < 2000) return 'Never'
  return d.toLocaleString(undefined, {
    year: 'numeric', month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit',
  })
}

// --- 1. Your account -------------------------------------------------------

const pw = reactive({ current: '', next: '', confirm: '' })
const pwBusy = ref(false)
const pwError = ref('')
const pwDone = ref(false)

const submitPassword = async () => {
  pwError.value = ''
  pwDone.value = false
  if (!pw.current || !pw.next) {
    pwError.value = 'Enter your current password and a new password.'
    return
  }
  if (pw.next !== pw.confirm) {
    pwError.value = 'The new password and the confirmation do not match.'
    return
  }
  pwBusy.value = true
  try {
    const result = await changePassword(pw.current, pw.next)
    // Tolerates either contract: a rejected promise, or a resolved result that
    // carries the server's error text.
    if (result && typeof result === 'object' && (result.ok === false || result.error)) {
      throw new Error(result.error || 'Could not change the password.')
    }
    pw.current = ''
    pw.next = ''
    pw.confirm = ''
    pwDone.value = true
    addToast('Password changed', 'success')
  } catch (e) {
    pwError.value = thrownMessage(e, 'Could not change the password.')
  } finally {
    pwBusy.value = false
  }
}

// --- 2. Users --------------------------------------------------------------

const users = ref([])
const usersLoading = ref(false)
const usersError = ref('')
const rowErrors = reactive({})
const busyId = ref(null)
const confirmId = ref(null)
const resetId = ref(null)
const resetValue = ref('')

// authState carries identity, not history, so the admin list is the only place a
// last sign-in exists. Everyone else sees "Never" until the server sends one.
const myLastLogin = computed(() => {
  if (me.value.lastLoginAt) return me.value.lastLoginAt
  const row = users.value.find((u) => isMe(u))
  return row ? row.lastLoginAt : null
})

const adminCount = computed(() => users.value.filter((u) => u.role === 'admin').length)
const lastAdmin = (u) => u.role === 'admin' && adminCount.value <= 1

// Guard rails are shown, not only enforced: the control is locked and the row
// says why, so nobody has to trigger a 409 to learn the rule.
const roleLocked = (u) => isMe(u) || lastAdmin(u)
const deleteLocked = (u) => isMe(u) || lastAdmin(u)

const rowNote = (u) => {
  if (isMe(u)) return 'You cannot change your own role or delete your own account. Ask another admin.'
  if (lastAdmin(u)) return 'This is the only admin. Add a second admin before changing or removing this account.'
  return ''
}

const setRowError = (id, message) => {
  rowErrors[id] = message
}
const clearRowError = (id) => {
  delete rowErrors[id]
}

const loadUsers = async () => {
  usersLoading.value = true
  usersError.value = ''
  try {
    const response = await fetch('/api/v1/users', { credentials: 'include', cache: 'no-store' })
    const body = await readBody(response)
    if (!response.ok) throw new Error(serverMessage(body, response, 'Could not load users.'))
    users.value = Array.isArray(body) ? body : []
  } catch (e) {
    users.value = []
    usersError.value = thrownMessage(e, 'Could not load users.')
  } finally {
    usersLoading.value = false
  }
}

const onRoleChange = async (u, role) => {
  if (!role || role === u.role) return
  clearRowError(u.id)
  busyId.value = u.id
  const previous = u.role
  try {
    const response = await fetch(`/api/v1/users/${u.id}`, {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ role }),
    })
    const body = await readBody(response)
    if (!response.ok) throw new Error(serverMessage(body, response, 'Could not change the role.'))
    u.role = body && body.role ? body.role : role
    addToast(`${u.username} is now ${u.role}`, 'success')
  } catch (e) {
    u.role = previous
    setRowError(u.id, thrownMessage(e, 'Could not change the role.'))
  } finally {
    busyId.value = null
  }
}

const openReset = (u) => {
  clearRowError(u.id)
  confirmId.value = null
  resetId.value = u.id
  resetValue.value = ''
}
const cancelReset = () => {
  resetId.value = null
  resetValue.value = ''
}

const submitReset = async (u) => {
  if (!resetValue.value) {
    setRowError(u.id, 'Enter a new password.')
    return
  }
  clearRowError(u.id)
  busyId.value = u.id
  try {
    const response = await fetch(`/api/v1/users/${u.id}`, {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: resetValue.value }),
    })
    const body = await readBody(response)
    if (!response.ok) throw new Error(serverMessage(body, response, 'Could not reset the password.'))
    cancelReset()
    addToast(`Password reset for ${u.username}`, 'success')
  } catch (e) {
    setRowError(u.id, thrownMessage(e, 'Could not reset the password.'))
  } finally {
    busyId.value = null
  }
}

// Two clicks, no browser dialog: the first arms the row, the second commits.
const startDelete = (u) => {
  clearRowError(u.id)
  resetId.value = null
  confirmId.value = u.id
}

const deleteUser = async (u) => {
  clearRowError(u.id)
  busyId.value = u.id
  try {
    const response = await fetch(`/api/v1/users/${u.id}`, { method: 'DELETE', credentials: 'include' })
    if (!response.ok) {
      const body = await readBody(response)
      throw new Error(serverMessage(body, response, 'Could not delete this user.'))
    }
    users.value = users.value.filter((row) => row.id !== u.id)
    confirmId.value = null
    addToast(`Deleted ${u.username}`, 'success')
  } catch (e) {
    setRowError(u.id, thrownMessage(e, 'Could not delete this user.'))
  } finally {
    busyId.value = null
  }
}

const draft = reactive({ username: '', password: '', role: 'viewer' })
const addBusy = ref(false)
const addError = ref('')

const addUser = async () => {
  addError.value = ''
  if (!draft.username.trim() || !draft.password) {
    addError.value = 'Enter a username and a password.'
    return
  }
  addBusy.value = true
  try {
    const response = await fetch('/api/v1/users', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: draft.username.trim(), password: draft.password, role: draft.role }),
    })
    const body = await readBody(response)
    if (!response.ok) throw new Error(serverMessage(body, response, 'Could not add this user.'))
    if (body && body.id != null) users.value = [...users.value, body]
    else await loadUsers()
    addToast(`Added ${draft.username.trim()}`, 'success')
    draft.username = ''
    draft.password = ''
    draft.role = 'viewer'
  } catch (e) {
    addError.value = thrownMessage(e, 'Could not add this user.')
  } finally {
    addBusy.value = false
  }
}

// --- 3. Monitoring ---------------------------------------------------------

const pausedKeys = ref([])
const monitoringLoading = ref(false)
const monitoringError = ref('')

const loadMonitoring = async () => {
  monitoringLoading.value = true
  monitoringError.value = ''
  try {
    const response = await fetch('/api/v1/monitoring', { credentials: 'include', cache: 'no-store' })
    const body = await readBody(response)
    if (!response.ok) throw new Error(serverMessage(body, response, 'Could not load paused checks.'))
    const list = body && (Array.isArray(body.paused) ? body.paused : body.disabled)
    pausedKeys.value = Array.isArray(list) ? [...list].sort() : []
  } catch (e) {
    pausedKeys.value = []
    monitoringError.value = thrownMessage(e, 'Could not load paused checks.')
  } finally {
    monitoringLoading.value = false
  }
}

// --- load ------------------------------------------------------------------

// The session may still be resolving when the view mounts, so the admin data is
// keyed off the resolved state rather than off mount.
watch(showAdmin, (isAdmin) => {
  if (!isAdmin) return
  loadUsers()
  loadMonitoring()
}, { immediate: true })

refreshAuth()
</script>

<style scoped>
/* ------------------------------------------------------------------ *
 *  Instrument panel.
 *
 *  The rest of this app already speaks equipment: uplink ladders, AP
 *  roster maps, status bars, capacity meters. This page had gone SaaS
 *  generic - four identical bordered boxes of forms - which is why it
 *  read as dead next to the pages around it. So it commits harder to
 *  that language than anywhere else in the app, because unlike the
 *  monitoring pages it has no live data to carry it.
 *
 *  Mono is the DISPLAY face here, not just a label font. No webfont is
 *  loaded on purpose: this is an offline LAN tool and a font CDN is a
 *  dependency it must not have.
 * ------------------------------------------------------------------ */

.settings-panel { width: 100%; display: flex; flex-direction: column; gap: 1rem; }

/* The mono tick label, used for every section head. The leading rule is
   the device that ties the page together. */
.tick {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  letter-spacing: 0.2em;
  text-transform: uppercase;
  font-weight: 700;
  color: hsl(var(--muted-foreground));
}
.tick::before {
  content: '';
  width: 14px;
  height: 2px;
  border-radius: 2px;
  background: hsl(var(--muted-foreground) / 0.55);
  flex-shrink: 0;
}
.tick-sm { margin-bottom: 0.55rem; }

/* --- masthead ----------------------------------------------------- */
.plate {
  display: flex;
  align-items: stretch;
  justify-content: space-between;
  gap: 1.25rem;
  flex-wrap: wrap;
  padding: 0.9rem 1.1rem;
  border: 1px solid hsl(var(--border));
  border-top: 1px solid hsl(var(--border));
  border-radius: 12px;
  background: hsl(var(--muted) / 0.28);
}
.plate-left { display: flex; align-items: center; gap: 0.85rem; min-width: 0; }
.back { color: hsl(var(--muted-foreground)); transition: color 0.15s ease; }
.back:hover { color: hsl(var(--foreground)); }
.back:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 3px; border-radius: 6px; }
.plate-h1 {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 1.35rem;
  font-weight: 700;
  letter-spacing: 0.02em;
  line-height: 1.05;
  margin-top: 0.3rem;
}

/* Three readouts, hairline-separated like a gauge cluster. */
.plate-right { display: flex; align-items: stretch; gap: 0; flex-wrap: wrap; }
.readout {
  display: flex;
  flex-direction: column;
  gap: 0.12rem;
  padding: 0 1.1rem;
  border-left: 1px solid hsl(var(--border));
  min-width: 8rem;
}
.readout:first-child { border-left: 0; }
.readout-k {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px; letter-spacing: 0.16em; text-transform: uppercase; font-weight: 700;
  color: hsl(var(--muted-foreground));
}
.readout-v { font-size: 1.05rem; font-weight: 700; line-height: 1.15; color: hsl(var(--foreground)); }
.readout-v.num { font-size: 1.5rem; font-variant-numeric: tabular-nums; letter-spacing: -0.01em; }
.readout-sub { display: inline-flex; align-items: center; gap: 0.4rem; }
.readout-dim { font-size: 0.66rem; color: hsl(var(--muted-foreground)); }
.readout.hot .readout-v { color: hsl(var(--foreground)); }

.chip {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px; letter-spacing: 0.1em; text-transform: uppercase; font-weight: 700;
  padding: 0.1rem 0.4rem; border-radius: 4px;
  border: 1px solid hsl(var(--border));
  color: hsl(var(--muted-foreground));
}
.chip-operator { color: hsl(var(--foreground)); background: hsl(var(--muted) / 0.7); }
.chip-admin { color: hsl(var(--background)); background: hsl(var(--foreground)); border-color: hsl(var(--foreground)); }

/* --- body layout -------------------------------------------------- */
.body {
  display: grid;
  grid-template-columns: minmax(320px, 400px) minmax(0, 1fr);
  gap: 1rem;
  align-items: start;
}
.body.solo { grid-template-columns: minmax(0, 720px); }
.rail, .main { display: flex; flex-direction: column; gap: 1rem; min-width: 0; }
@media (max-width: 1040px) {
  .body, .body.solo { grid-template-columns: minmax(0, 1fr); }
}

/* Blocks are open, not boxed-in: a top hairline and a head, so the page
   reads as one panel with sections rather than a tray of cards. */
.block {
  border: 1px solid hsl(var(--border));
  border-radius: 12px;
  background: hsl(var(--muted) / 0.24);
  padding: 0.85rem 1rem 1rem;
}
.block-head {
  display: flex; align-items: center; justify-content: space-between; gap: 0.75rem;
  padding-bottom: 0.7rem; margin-bottom: 0.85rem;
  border-bottom: 1px solid hsl(var(--border));
}
.head-note {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.66rem; color: hsl(var(--muted-foreground));
}
.block-note {
  margin: 0.8rem 0 0; font-size: 0.72rem; line-height: 1.5;
  color: hsl(var(--muted-foreground));
}

/* --- the clearance bay (signature) -------------------------------- */
.bay { display: flex; flex-direction: column; }
.bay-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) repeat(3, 3.2rem);
  align-items: center;
  gap: 0.25rem;
}
.bay-row + .bay-row .bay-label { border-top: 1px solid hsl(var(--border) / 0.55); }
.bay-label {
  font-size: 0.78rem; color: hsl(var(--foreground));
  padding: 0.5rem 0.6rem 0.5rem 0;
}
.bay-head .bay-label { padding: 0; }
.bay-ch {
  display: flex; align-items: center; justify-content: center;
  align-self: stretch;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px; letter-spacing: 0.1em; text-transform: uppercase; font-weight: 700;
  color: hsl(var(--muted-foreground));
}
.bay-head .bay-ch { padding-bottom: 0.5rem; }
/* The lit channel: a continuous band down the reader's own column, not a
   per-cell highlight that would read as stripes. */
.bay-ch.mine { background: hsl(var(--muted) / 0.55); color: hsl(var(--foreground)); }
.bay-head .bay-ch.mine { border-radius: 8px 8px 0 0; }
.bay-row:last-child .bay-ch.mine { border-radius: 0 0 8px 8px; }

.jack {
  width: 10px; height: 10px; border-radius: 999px;
  background: transparent;
  box-shadow: inset 0 0 0 1.5px hsl(var(--muted-foreground) / 0.38);
}
.bay-ch.on .jack { background: hsl(var(--foreground)); box-shadow: none; }
.bay-ch.mine.on .jack { box-shadow: 0 0 0 3px rgb(90 160 107 / 0.22); }

/* --- roster ------------------------------------------------------- */
.roster { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; }
.crew {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto auto;
  align-items: center;
  gap: 0.75rem;
  padding: 0.6rem 0;
  border-top: 1px solid hsl(var(--border) / 0.55);
}
.crew:first-child { border-top: 0; }
.crew.armed { background: rgb(239 107 83 / 0.06); }
.crew-mark {
  width: 30px; height: 30px; flex-shrink: 0;
  display: inline-flex; align-items: center; justify-content: center;
  border-radius: 8px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.66rem; font-weight: 800;
  background: hsl(var(--background)); border: 1px solid hsl(var(--border));
  color: hsl(var(--foreground));
}
.crew-id { display: flex; flex-direction: column; gap: 0.1rem; min-width: 0; }
.crew-name { font-size: 0.88rem; font-weight: 700; display: inline-flex; align-items: center; gap: 0.4rem; }
.crew-you {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 8px; letter-spacing: 0.12em; text-transform: uppercase; font-weight: 800;
  color: hsl(var(--foreground));
}
.crew-seen {
  font-size: 0.66rem; color: hsl(var(--muted-foreground));
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.crew-role { min-width: 7rem; }
/* Actions stay out of the way until the row is wanted, so a column of
   Delete buttons is not the loudest thing in the panel. */
.crew-do { display: inline-flex; gap: 0.35rem; flex-wrap: wrap; justify-content: flex-end; opacity: 0.45; transition: opacity 0.15s ease; }
.crew:hover .crew-do, .crew:focus-within .crew-do, .crew.armed .crew-do { opacity: 1; }
.crew-drawer { grid-column: 1 / -1; padding: 0.15rem 0 0.35rem; }
.reset-form { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }

/* --- paused keys -------------------------------------------------- */
.keys { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: 0.4rem; }
.key {
  display: inline-flex; align-items: center; gap: 0.4rem;
  font-size: 0.74rem;
  padding: 0.2rem 0.55rem;
  border: 1px solid rgb(224 160 88 / 0.35);
  background: rgb(224 160 88 / 0.08);
  border-radius: 999px;
  color: hsl(var(--foreground));
}
.key:hover { border-color: rgb(224 160 88 / 0.7); }
.key:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; }
.key-led { width: 6px; height: 6px; border-radius: 999px; background: hsl(var(--muted-foreground)); flex-shrink: 0; }

/* --- forms -------------------------------------------------------- */
.stack { display: flex; flex-direction: column; gap: 0.7rem; }
.add { margin-top: 1rem; padding-top: 0.9rem; border-top: 1px solid hsl(var(--border)); }
.fields { display: grid; gap: 0.6rem; grid-template-columns: minmax(0, 1fr); }
.fields-3 { grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); }
.lbl { display: flex; flex-direction: column; gap: 0.3rem; min-width: 0; }
.lbl > span {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px; letter-spacing: 0.14em; text-transform: uppercase; font-weight: 700;
  color: hsl(var(--muted-foreground));
}
.lbl-inline { flex-direction: row; align-items: center; gap: 0.5rem; }
.field {
  width: 100%;
  background: hsl(var(--background));
  border: 1px solid hsl(var(--border));
  border-radius: 8px;
  padding: 0.4rem 0.55rem;
  font-size: 0.85rem;
  color: hsl(var(--foreground));
}
.field:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 1px; }
.field:disabled { opacity: 0.55; }
.field-sm { width: auto; min-width: 8.5rem; padding: 0.25rem 0.45rem; font-size: 0.8rem; }
.row-actions-left { display: flex; align-items: center; gap: 0.7rem; flex-wrap: wrap; }
.hint { font-size: 0.68rem; color: hsl(var(--muted-foreground)); }

.btn {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.7rem; letter-spacing: 0.06em; text-transform: uppercase; font-weight: 700;
  padding: 0.35rem 0.7rem;
  border-radius: 7px;
  border: 1px solid hsl(var(--border));
  background: transparent;
  color: hsl(var(--muted-foreground));
  cursor: pointer;
  transition: color 0.15s ease, background 0.15s ease, border-color 0.15s ease;
}
.btn:hover:not(:disabled) { color: hsl(var(--foreground)); background: hsl(var(--accent) / 0.55); }
.btn:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 1px; }
.btn:disabled { opacity: 0.45; cursor: default; }
.btn-primary { color: hsl(var(--background)); background: hsl(var(--foreground)); border-color: hsl(var(--foreground)); }
.btn-primary:hover:not(:disabled) { color: #1a1206; background: #e8b673; }
.btn-danger { color: #1a0d0a; background: #ef6b53; border-color: #ef6b53; }
.btn-danger:hover:not(:disabled) { color: #1a0d0a; background: #f2836e; }

/* --- notices and messages ----------------------------------------- */
.notice { border: 1px dashed hsl(var(--border)); border-radius: 12px; padding: 1.25rem; }
.notice-title { font-weight: 700; margin-bottom: 0.35rem; }
.notice-body { font-size: 0.85rem; color: hsl(var(--muted-foreground)); max-width: 62ch; line-height: 1.5; }
.msg { font-size: 0.76rem; line-height: 1.45; margin: 0.4rem 0 0; }
.msg-error { color: hsl(var(--destructive)); }
.msg-dim { color: hsl(var(--muted-foreground)); }
.msg-ok { color: hsl(var(--foreground)); margin: 0; }

.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.sr-only {
  position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px;
  overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0;
}

/* One quiet arrival, not a sequence of effects. */
@media (prefers-reduced-motion: no-preference) {
  .plate.is-in { animation: plate-in 0.32s cubic-bezier(0.22, 1, 0.36, 1) both; }
  .block { animation: block-in 0.3s cubic-bezier(0.22, 1, 0.36, 1) both; }
  .rail .block:nth-child(2) { animation-delay: 0.05s; }
  .main .block:nth-child(2) { animation-delay: 0.08s; }
}
@keyframes plate-in { from { opacity: 0; transform: translateY(-4px); } to { opacity: 1; transform: none; } }
@keyframes block-in { from { opacity: 0; transform: translateY(6px); } to { opacity: 1; transform: none; } }
</style>
