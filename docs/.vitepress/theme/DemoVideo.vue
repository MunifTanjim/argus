<script setup lang="ts">
import { ref, onMounted } from "vue";
import { withBase } from "vitepress";

const props = defineProps<{ src: string; alt?: string; portrait?: boolean }>();

// No autoplay attribute (SSR-safe, paused by default). On mount, play only when
// the visitor hasn't asked to reduce motion; otherwise leave it paused with
// controls so they can play it themselves.
const el = ref<HTMLVideoElement>();
const reduce = ref(false);

onMounted(() => {
  reduce.value = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (!reduce.value) el.value?.play().catch(() => {});
});
</script>

<template>
  <figure class="demo-video" :class="{ portrait: props.portrait }">
    <video
      ref="el"
      :src="withBase(props.src)"
      :controls="reduce"
      loop
      muted
      playsinline
      preload="metadata"
      :aria-label="props.alt"
    ></video>
    <figcaption v-if="props.alt">{{ props.alt }}</figcaption>
  </figure>
</template>
