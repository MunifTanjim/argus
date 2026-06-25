import { defineConfig } from "vitepress";

export default defineConfig({
  cleanUrls: true,
  lastUpdated: true,

  title: "Argus",
  description:
    "Watch and control all your AI coding sessions — from one place.",

  head: [
    [
      "link",
      {
        rel: "icon",
        href: "/logo.png",
      },
    ],
  ],

  sitemap: {
    hostname: "https://argus.muniftanjim.dev",
  },

  themeConfig: {
    logo: "/logo.png",

    nav: [
      { text: "Getting Started", link: "/getting-started/introduction" },
      { text: "Guide", link: "/guide/single-machine" },
    ],

    sidebar: [
      {
        text: "Getting Started",
        items: [
          { text: "Introduction", link: "/getting-started/introduction" },
          { text: "Installation", link: "/getting-started/installation" },
          { text: "Configuration", link: "/getting-started/configuration" },
        ],
      },
      {
        text: "Guide",
        items: [
          { text: "Single Machine", link: "/guide/single-machine" },
          { text: "Multi Machine", link: "/guide/multi-machine" },
          { text: "Gateway Tunnel", link: "/guide/gateway-tunnel" },
          { text: "TUI", link: "/guide/tui" },
          { text: "Mobile App", link: "/guide/mobile-app" },
        ],
      },
    ],

    socialLinks: [
      {
        icon: "github",
        link: "https://github.com/MunifTanjim/argus",
      },
    ],

    editLink: {
      pattern: "https://github.com/MunifTanjim/argus/edit/main/docs/:path",
    },

    search: {
      provider: "local",
    },
  },
});
