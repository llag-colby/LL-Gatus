<template>
  <!-- Some endpoint kinds have a purpose-built drill-in instead of the generic
       network detail page: phones get an inventory table, firewall gets its WAN
       uplinks, wireless gets its AP fleet. Everything else uses EndpointDetails. -->
  <PhoneDetails v-if="kind === 'phones'" />
  <FirewallDetails v-else-if="kind === 'firewall'" />
  <WirelessDetails v-else-if="kind === 'wireless'" />
  <EndpointDetails v-else />
</template>

<script setup>
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import EndpointDetails from './EndpointDetails.vue'
import PhoneDetails from './PhoneDetails.vue'
import FirewallDetails from './FirewallDetails.vue'
import WirelessDetails from './WirelessDetails.vue'

const route = useRoute()

// The key is slug(group)_slug(name), so the group prefix identifies the kind.
// See config.yaml: groups "Phones", "Firewall" and "Wireless".
const kind = computed(() => {
  const key = route.params.key || ''
  if (key.startsWith('phones_')) return 'phones'
  if (key.startsWith('firewall_')) return 'firewall'
  if (key.startsWith('wireless_')) return 'wireless'
  return 'endpoint'
})
</script>
