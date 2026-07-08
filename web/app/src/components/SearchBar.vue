<template>
  <div class="flex flex-wrap items-center gap-2 sm:gap-3">
    <!-- Search -->
    <div class="relative" data-tooltip="Search endpoints by name or group" data-tip-pos="bottom">
      <Search class="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground pointer-events-none" />
      <label for="search-input" class="sr-only">Search endpoints</label>
      <Input
        id="search-input"
        v-model="controls.searchQuery"
        type="text"
        placeholder="Search endpoints..."
        class="pl-9 h-9 w-full sm:w-52 lg:w-64 text-sm"
      />
    </div>

    <!-- Filter -->
    <div class="flex items-center gap-1.5" data-tooltip="Filter by health" data-tip-pos="bottom">
      <Filter class="h-4 w-4 text-muted-foreground shrink-0" />
      <Select
        v-model="filterBy"
        :options="filterOptions"
        placeholder="None"
        class="w-[118px] md:w-[130px]"
        @update:model-value="handleFilterChange"
      />
    </div>

    <!-- Sort -->
    <div class="flex items-center gap-1.5" data-tooltip="Sort endpoints" data-tip-pos="bottom">
      <ArrowUpDown class="h-4 w-4 text-muted-foreground shrink-0" />
      <Select
        v-model="sortBy"
        :options="sortOptions"
        placeholder="Name"
        class="w-[104px] md:w-[112px]"
        @update:model-value="handleSortChange"
      />
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { Search, Filter, ArrowUpDown } from 'lucide-vue-next'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { controls } from '@/store'

const filterBy = ref(controls.filterBy)
const sortBy = ref(controls.sortBy)

const filterOptions = [
  { label: 'None', value: 'none' },
  { label: 'Failing', value: 'failing' },
  { label: 'Unstable', value: 'unstable' }
]

const sortOptions = [
  { label: 'Name', value: 'name' },
  { label: 'Health', value: 'health' }
]

const applyFilter = (value) => {
  controls.filterBy = value
  controls.showOnlyFailing = value === 'failing'
  controls.showRecentFailures = value === 'unstable'
}

const applySort = (value) => {
  controls.sortBy = value
  controls.groupByGroup = value === 'group'
}

const handleFilterChange = (value) => {
  filterBy.value = value
  localStorage.setItem('gatus:filter-by', value)
  applyFilter(value)
}

const handleSortChange = (value) => {
  sortBy.value = value
  localStorage.setItem('gatus:sort-by', value)
  applySort(value)
}

onMounted(() => {
  // Sync the store with the saved filter/sort state on load.
  applyFilter(filterBy.value)
  applySort(sortBy.value)
})
</script>
