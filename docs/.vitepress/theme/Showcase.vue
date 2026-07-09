<script setup lang="ts">
import { ref, onMounted } from "vue";
import { withBase } from "vitepress";

// Which pane is in front, and whether the showcase is zoomed in. It starts
// compact; the first click zooms in and focuses the clicked pane, clicking the
// focused pane zooms back out, and clicking the other pane swaps focus.
const front = ref<"tui" | "mobile">("tui");
const zoomed = ref(false);

// Autoplay only when the visitor hasn't asked to reduce motion; otherwise the
// videos stay paused (the CSS also drops the swap/zoom transitions).
const tuiVid = ref<HTMLVideoElement>();
const appVid = ref<HTMLVideoElement>();
onMounted(() => {
  if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
  tuiVid.value?.play().catch(() => {});
  appVid.value?.play().catch(() => {});
});

function activate(who: "tui" | "mobile") {
  if (!zoomed.value) {
    zoomed.value = true;
    front.value = who;
  } else if (front.value === who) {
    zoomed.value = false;
  } else {
    front.value = who;
  }
}
</script>

<template>
  <div class="showcase" :class="{ zoomed }">
    <div class="showcase-stage">
      <button
        type="button"
        class="pane tui"
        :class="front === 'tui' ? 'front' : 'back'"
        :aria-pressed="front === 'tui'"
        aria-label="Bring the terminal UI to the front"
        @click="activate('tui')"
      >
        <video
          ref="tuiVid"
          :src="withBase('/screenshots/demo-tui.mp4')"
          loop
          muted
          playsinline
          preload="metadata"
        ></video>
      </button>

      <button
        type="button"
        class="pane mobile"
        :class="front === 'mobile' ? 'front' : 'back'"
        :aria-pressed="front === 'mobile'"
        aria-label="Bring the mobile app to the front"
        @click="activate('mobile')"
      >
        <video
          ref="appVid"
          :src="withBase('/screenshots/demo-app.mp4')"
          loop
          muted
          playsinline
          preload="metadata"
        ></video>
      </button>
    </div>
  </div>
</template>
