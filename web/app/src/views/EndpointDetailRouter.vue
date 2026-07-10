<template>
  <!-- Phones endpoints get a completely different drill-in (an inventory table);
       everything else uses the normal network detail page. -->
  <PhoneDetails v-if="isPhones" />
  <EndpointDetails v-else />
</template>

<script setup>
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import EndpointDetails from './EndpointDetails.vue'
import PhoneDetails from './PhoneDetails.vue'

const route = useRoute()
// Phone endpoints are keyed phones_<location> (group "Phones"); see config.yaml.
const isPhones = computed(() => (route.params.key || '').startsWith('phones_'))
</script>
