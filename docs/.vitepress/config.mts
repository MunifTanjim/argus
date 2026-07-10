import { copyFileSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vitepress";

const hostname = "https://argus.muniftanjim.dev";

// Served at the site root so `curl https://argus.muniftanjim.dev/install.sh` works
const installScriptPath = fileURLToPath(
  new URL("../../scripts/install.sh", import.meta.url),
);

export default defineConfig({
  cleanUrls: true,
  lastUpdated: true,

  // Local Superpowers scratch (brainstorm/plan notes); not part of the docs site.
  srcExclude: ["superpowers/**"],

  title: "Argus",
  description:
    "Watch and control all your AI agents.",

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
    hostname,
  },

  buildEnd(siteConfig) {
    copyFileSync(installScriptPath, `${siteConfig.outDir}/install.sh`);
  },

  vite: {
    plugins: [
      {
        name: "serve-install-script",
        configureServer(server) {
          server.middlewares.use("/install.sh", (_req, res) => {
            res.setHeader("Content-Type", "text/plain");
            res.end(readFileSync(installScriptPath));
          });
        },
      },
    ],
  },

  transformHead({ pageData, siteData }) {
    const image = `${hostname}/og.png`;
    const isHome = pageData.frontmatter.layout === "home";
    const raw = (pageData.frontmatter.title as string) || pageData.title;
    const title = raw && !isHome ? `${raw} | Argus` : "Argus";
    const description =
      (pageData.frontmatter.description as string) || siteData.description;
    const path = pageData.relativePath
      .replace(/(^|\/)index\.md$/, "$1")
      .replace(/\.md$/, "");
    const url = `${hostname}/${path}`;
    return [
      ["meta", { property: "og:type", content: "website" }],
      ["meta", { property: "og:site_name", content: "Argus" }],
      ["meta", { property: "og:title", content: title }],
      ["meta", { property: "og:description", content: description }],
      ["meta", { property: "og:url", content: url }],
      ["meta", { property: "og:image", content: image }],
      ["meta", { property: "og:image:width", content: "1200" }],
      ["meta", { property: "og:image:height", content: "630" }],
      ["meta", { property: "og:image:alt", content: "Argus" }],
      ["meta", { name: "twitter:card", content: "summary_large_image" }],
      ["meta", { name: "twitter:title", content: title }],
      ["meta", { name: "twitter:description", content: description }],
      ["meta", { name: "twitter:image", content: image }],
    ];
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
      {
        icon: "discord",
        link: "https://go.muniftanjim.dev/discord",
      },
      {
        icon: "buymeacoffee",
        link: "https://buymeacoffee.com/muniftanjim",
      },
      {
        icon: "patreon",
        link: "https://www.patreon.com/muniftanjim",
      },
    ],

    editLink: {
      pattern: "https://github.com/MunifTanjim/argus/edit/main/docs/:path",
    },

    footer: {
      message:
        'Released under the <a href="https://github.com/MunifTanjim/argus/blob/main/LICENSE">MIT License</a>. · <a href="/privacy">Privacy</a>',
      copyright: "© 2026 Munif Tanjim",
    },

    search: {
      provider: "local",
    },
  },
});
