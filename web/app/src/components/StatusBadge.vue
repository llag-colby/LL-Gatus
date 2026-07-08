<template>
  <Badge :variant="variant" class="flex items-center gap-1">
    {{ label }}
  </Badge>
</template>

<script setup>
import { computed } from 'vue'
import { Badge } from '@/components/ui/badge'

const props = defineProps({
  status: {
    type: String,
    required: true,
    validator: (value) => ['healthy', 'unhealthy', 'degraded', 'unknown'].includes(value)
  }
})

const variant = computed(() => {
  switch (props.status) {
    case 'healthy':
      return 'success'
    case 'unhealthy':
      return 'destructive'
    case 'degraded':
      return 'warning'
    default:
      return 'secondary'
  }
})

const label = computed(() => {
  switch (props.status) {
    case 'healthy':
      return 'Healthy'
    case 'unhealthy':
      return 'Unhealthy'
    case 'degraded':
      return 'Degraded'
    default:
      return 'Unknown'
  }
})
</script>