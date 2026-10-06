<template>
  <!-- Some endpoint kinds have a purpose-built drill-in instead of the generic
       network detail page: phones get an inventory table, firewall gets its WAN
       uplinks, wireless gets its AP fleet, an SMB share gets its path, the rows
       that share its probe and what the port check does not cover, and a
       hypervisor gets its CPU, memory, volumes and guest inventory. Everything
       else uses EndpointDetails. -->
  <PhoneDetails v-if="kind === 'phones'" />
  <FirewallDetails v-else-if="kind === 'firewall'" />
  <WirelessDetails v-else-if="kind === 'wireless'" />
  <SmbShareDetails v-else-if="kind === 'smb'" />
  <HypervisorDetails v-else-if="kind === 'hv'" />
  <EndpointDetails v-else />
</template>

<script setup>
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import EndpointDetails from './EndpointDetails.vue'
import PhoneDetails from './PhoneDetails.vue'
import FirewallDetails from './FirewallDetails.vue'
import WirelessDetails from './WirelessDetails.vue'
import SmbShareDetails from './SmbShareDetails.vue'
import HypervisorDetails from './HypervisorDetails.vue'
import { isSmbShareKey } from '@/utils/smbShares'
import { isHypervisorKey } from '@/utils/hypervisors'

const route = useRoute()

// The key is slug(group)_slug(name), so the group prefix identifies the kind.
// See config.yaml: groups "Phones", "Firewall" and "Wireless".
//
// SMB and hypervisors are the exception: there the kind is the endpoint NAME
// ("SMB Shares", "Hypervisors") and the group half is the drive letter or the
// host name, so they match on the suffix instead.
const kind = computed(() => {
  const key = route.params.key || ''
  if (key.startsWith('phones_')) return 'phones'
  if (key.startsWith('firewall_')) return 'firewall'
  if (key.startsWith('wireless_')) return 'wireless'
  if (isSmbShareKey(key)) return 'smb'
  if (isHypervisorKey(key)) return 'hv'
  return 'endpoint'
})
</script>
