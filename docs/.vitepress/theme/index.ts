import DefaultTheme from "vitepress/theme";
import { h } from "vue";
import "./custom.css";
import Showcase from "./Showcase.vue";
import DemoVideo from "./DemoVideo.vue";
import ObfuscatedEmail from "./ObfuscatedEmail.vue";

// Show the TUI (and the mobile app peeking behind it) right under the hero.
export default {
  extends: DefaultTheme,
  enhanceApp({ app }) {
    app.component("DemoVideo", DemoVideo);
    app.component("ObfuscatedEmail", ObfuscatedEmail);
  },
  Layout() {
    return h(DefaultTheme.Layout, null, {
      "home-hero-after": () => h(Showcase),
    });
  },
};
