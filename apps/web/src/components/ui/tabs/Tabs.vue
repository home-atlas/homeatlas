<script setup lang="ts">
import { TabsRoot, useForwardPropsEmits, type TabsRootProps, type TabsRootEmits } from 'reka-ui'
const props = defineProps<TabsRootProps>()
const emits = defineEmits<TabsRootEmits>()
const forwarded = useForwardPropsEmits(props, emits)
</script>
<template><TabsRoot data-slot="tabs" v-bind="forwarded"><slot /></TabsRoot></template>
