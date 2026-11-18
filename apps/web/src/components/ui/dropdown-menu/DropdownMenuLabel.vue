<script setup lang="ts">
import { DropdownMenuLabel, type DropdownMenuLabelProps } from 'reka-ui'
const props = defineProps<DropdownMenuLabelProps>()
</script>
<template><DropdownMenuLabel data-slot="dropdown-menu-label" class="px-3 py-2" v-bind="props"><slot /></DropdownMenuLabel></template>
