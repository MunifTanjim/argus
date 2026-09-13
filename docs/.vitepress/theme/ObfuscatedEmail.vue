<script setup lang="ts">
import { ref, computed, onMounted } from "vue";

const props = defineProps<{ user: string; domain: string }>();

// The address is assembled on mount, never during SSR, so the pre-rendered HTML
// carries nothing a scraper can match. Starting empty keeps the first client
// render identical to the server's, which avoids a hydration mismatch.
const address = ref("");

// Some mail clients read a literal "+" in a mailto href as a space.
const mailto = computed(
  () => `mailto:${encodeURIComponent(props.user)}@${props.domain}`,
);

const spelled = computed(() =>
  `${props.user}@${props.domain}`
    .replaceAll("+", " [plus] ")
    .replaceAll("@", " [at] ")
    .replaceAll(".", " [dot] "),
);

onMounted(() => {
  address.value = `${props.user}@${props.domain}`;
});
</script>

<template>
  <span class="obf-email">
    <a v-if="address" :href="mailto">{{ address }}</a>
    <noscript>{{ spelled }}</noscript>
  </span>
</template>
