# Changelog

## [0.0.13](https://github.com/MunifTanjim/argus/compare/app-0.0.12...app-0.0.13) (2026-07-13)


### Features

* render skill load as user chunk item ([e0bd426](https://github.com/MunifTanjim/argus/commit/e0bd426c34eb2059ee7a97f9d8c2b8417d754557))
* show current git branch in session view ([b144e70](https://github.com/MunifTanjim/argus/commit/b144e70243ba83cd8a2dbdd3c22d16f252a1e539))
* **app:** make transcript text selectable and links tappable ([8afae7f](https://github.com/MunifTanjim/argus/commit/8afae7f516f2f22c03a45b0dff9274d14e57c6d9))
* **app:** render plan and web tool results as markdown ([58c09d0](https://github.com/MunifTanjim/argus/commit/58c09d01ec226380034ea85c68825fb7c09952f5))
* **app:** resume sessions from history ([dbdbe39](https://github.com/MunifTanjim/argus/commit/dbdbe398d1053575ec73f5dbe7c607b5d5bbee92))
* **app:** show claude session name in ui and notifications ([35fe48f](https://github.com/MunifTanjim/argus/commit/35fe48f26a3a6b6b83055410120f3c4c974d95a8))


### Bug Fixes

* encode modified cursor & nav keys in virtual terminal ([ff6b2f1](https://github.com/MunifTanjim/argus/commit/ff6b2f14e7eb9844f86827e5b5c5a08daa3e42c2))
* **app:** bundle fonts for glyphs in terminal ([5ce57de](https://github.com/MunifTanjim/argus/commit/5ce57de3a6e05e60a8016ed7ef044426d20e96d0))
* **app:** dismiss connection view after connecting ([b46b6b8](https://github.com/MunifTanjim/argus/commit/b46b6b87fdfb8d937ca43a1a6a32d595eadec356))
* **app:** format chunk duration with minutes ([3314621](https://github.com/MunifTanjim/argus/commit/3314621df54a509e348373e07dbc7514d8a21325))
* **app:** show public key for saved ssh keys ([a112b22](https://github.com/MunifTanjim/argus/commit/a112b225316499ee1d2901af546ffb6934db5219))
* **claude:** surface sessions stuck at startup prompt ([99f0318](https://github.com/MunifTanjim/argus/commit/99f03189a70fe55f29f554635864fd2fadd5a44d))

## [0.0.12](https://github.com/MunifTanjim/argus/compare/app-0.0.11...app-0.0.12) (2026-07-09)


### Features

* virtual terminal emulator for live screen ([f963ca3](https://github.com/MunifTanjim/argus/commit/f963ca316d0e1e29c2351829136999d49b1e5e59))
* **app:** collapse tool calls in AI chunks ([8996703](https://github.com/MunifTanjim/argus/commit/89967035ca6cd91deb17b3ea53437f24000c7aca))
* **app:** render pretty tool details - Skill ([4ed38be](https://github.com/MunifTanjim/argus/commit/4ed38beb4e88d320910fbfb663ac704800718835))


### Bug Fixes

* tweak push notification delivery and ux on cold start ([65a588b](https://github.com/MunifTanjim/argus/commit/65a588b0916c5cd2c7a81e87612b8226d873ea4c))

## [0.0.11](https://github.com/MunifTanjim/argus/compare/app-0.0.10...app-0.0.11) (2026-07-07)


### Features

* choose agent when spawning a session ([2ba126d](https://github.com/MunifTanjim/argus/commit/2ba126dff3d2da765832fea10f217db58e1ee843))
* compute model name and color server-side ([a1e9572](https://github.com/MunifTanjim/argus/commit/a1e9572ffd33a9a382f3af860203cd4e818fbfd6))
* multi-agent history tab (codex + antigravity) ([5a072a6](https://github.com/MunifTanjim/argus/commit/5a072a67aebe325f5a460fc98f072f2dc8c746d3))
* prepare hook handler system for multiple agents ([80befe9](https://github.com/MunifTanjim/argus/commit/80befe952b8b8d16e79a5c6f08ee7126897bebe8))
* show session agent on cards ([6ca898b](https://github.com/MunifTanjim/argus/commit/6ca898b73f11a1a8f306ce5384c9874e060b1e21))
* **antigravity:** add initial adapter ([8cc773f](https://github.com/MunifTanjim/argus/commit/8cc773f2732b22fba45c598efb588beef3e825f0))
* **app:** drill into thinking items that carry text ([c8359d0](https://github.com/MunifTanjim/argus/commit/c8359d034e4f27b6514a75a9f95493d493b7ddda))
* **claude:** emit shell and skill chunks ([062a726](https://github.com/MunifTanjim/argus/commit/062a726ecac1e1da5b901c3b1eb4b52fee2bc695))
* **claude:** render teammate messages ([b0c081e](https://github.com/MunifTanjim/argus/commit/b0c081e32e0ecc0bf0079d20aa06693a5f1793c9))
* **claude:** render tool items with a tool registry ([cc81906](https://github.com/MunifTanjim/argus/commit/cc81906e104e81a73e6b0c8cf486fbd39b1b5465))
* **codex:** add initial adapter ([e519056](https://github.com/MunifTanjim/argus/commit/e5190561a8ae40b9b5669c0377688f9bc822b0de))
* **codex:** render tool items with a tool registry ([13266a2](https://github.com/MunifTanjim/argus/commit/13266a222f9dd2787c1e1256225c886f07729452))


### Bug Fixes

* **app:** keep history session list above nav bar ([0060fae](https://github.com/MunifTanjim/argus/commit/0060fae1e25da55bb422045a577a5a558dec41ca))
* **claude:** render skill tool properly ([115372e](https://github.com/MunifTanjim/argus/commit/115372ee219434f39ca9322faafbfaced6b09f31))

## [0.0.10](https://github.com/MunifTanjim/argus/compare/app-0.0.9...app-0.0.10) (2026-07-03)


### Features

* render system chunks as bordered cards ([48161a5](https://github.com/MunifTanjim/argus/commit/48161a5f6149499976f468b7e9ceaf9a970ac1a4))
* **app:** add code block controls for header, wrap, copy, and line numbers ([22b9146](https://github.com/MunifTanjim/argus/commit/22b91460824d284520ea2d4daa727e2fa66e8654))
* **app:** collapse long user messages behind a show more toggle ([f22549a](https://github.com/MunifTanjim/argus/commit/f22549a5c8bd07369908006e4f3f27e48a99dba2))
* **app:** improve ai chunk header ([e62d22f](https://github.com/MunifTanjim/argus/commit/e62d22fdbe4746a0bd640f9d8b8619c10633e2c4))
* **app:** improve live screen view ([c0b4c42](https://github.com/MunifTanjim/argus/commit/c0b4c4253a2833d8be53ad72ba18224f1d2b37bd))
* **app:** pretty-print model names in session and chunk displays ([8d96ba6](https://github.com/MunifTanjim/argus/commit/8d96ba60dd05a533aa9442ff4297d1f938e40b9c))
* **app:** render tool detail with syntax-highlighted code blocks ([c6d3ec2](https://github.com/MunifTanjim/argus/commit/c6d3ec28d9719e6ff8f6a2394f7ce38c527429b7))
* **app:** render tool results as markdown with language detection ([490f73f](https://github.com/MunifTanjim/argus/commit/490f73f89de05caf48dc333078093882a179a08c))
* **claudecode:** show session recaps in the transcript ([f385bb5](https://github.com/MunifTanjim/argus/commit/f385bb5dd04eae63fe366dbe24698477c3cf7c04))


### Bug Fixes

* **app:** dedupe project cwds so new session picker doesn't crash ([2244b36](https://github.com/MunifTanjim/argus/commit/2244b36a1af7df795f898a098a329c625511981a))
* **app:** show system card time in local timezone ([0563937](https://github.com/MunifTanjim/argus/commit/056393793add775f0d215a25fd60ce20905f61f8))
* **claudecode:** handle /clear transcript swap correctly ([1ad0556](https://github.com/MunifTanjim/argus/commit/1ad055606ef9cba1925945c5026878242d2ac59b))

## [0.0.9](https://github.com/MunifTanjim/argus/compare/app-0.0.8...app-0.0.9) (2026-07-02)


### Bug Fixes

* **app:** stop keyboard pushing connection form off-screen ([c4bc4d9](https://github.com/MunifTanjim/argus/commit/c4bc4d930266180a809546fd8a8cac6e447ab153))

## [0.0.8](https://github.com/MunifTanjim/argus/compare/app-0.0.7...app-0.0.8) (2026-07-02)


### Features

* **app:** support ssh connection ([77813af](https://github.com/MunifTanjim/argus/commit/77813afbc8a03c0a87b38fedb2610eb24eb7ca54))

## [0.0.7](https://github.com/MunifTanjim/argus/compare/app-0.0.6...app-0.0.7) (2026-06-30)


### Features

* add server.info with version and connected nodes ([2977358](https://github.com/MunifTanjim/argus/commit/29773583d86f3b5788068ccf189f642374c1912f))
* gate spawn-session on per-node tmux availability ([68cfb18](https://github.com/MunifTanjim/argus/commit/68cfb18004e3b7e3370d42e491d4fd99e8fd9671))
* spawn new sessions with a dir picker and initial prompt ([f95e1af](https://github.com/MunifTanjim/argus/commit/f95e1aff41bfebc65019f8d2aeaec7744b3d50b9))


### Bug Fixes

* **push:** recover from gone endpoints by minting a fresh one ([48dfd9b](https://github.com/MunifTanjim/argus/commit/48dfd9b6261c7e188f66e6797233764f09124c65))

## [0.0.6](https://github.com/MunifTanjim/argus/compare/app-0.0.5...app-0.0.6) (2026-06-29)


### Features

* support non-tmux claude sessions ([9688398](https://github.com/MunifTanjim/argus/commit/96883981a8873bb9d61aa14087bc82c8d5b25f62))
* **app:** auto-dismiss and de-duplicate session push notifications ([2a2fd77](https://github.com/MunifTanjim/argus/commit/2a2fd773c86f7779429bcd747b93ad4f981f2e48))

## [0.0.5](https://github.com/MunifTanjim/argus/compare/app-0.0.4...app-0.0.5) (2026-06-26)


### Features

* **app:** drill into nested subagents in history sessions ([e1561d0](https://github.com/MunifTanjim/argus/commit/e1561d070d9f933c9c661953043e14a70eb45518))
* **app:** group history projects by node with per-node headers ([cc0d0dc](https://github.com/MunifTanjim/argus/commit/cc0d0dc66b22070218ca526011795f9328519f9a))
* **app:** show node on Needs you session cards ([f9d40bd](https://github.com/MunifTanjim/argus/commit/f9d40bd22f5fc8f3588451c6126f9bc041ec4105))


### Bug Fixes

* **app:** use auto height for response sheet ([ea26c9c](https://github.com/MunifTanjim/argus/commit/ea26c9c2f96c475167e3e316881000a4e0ad2a06))

## [0.0.4](https://github.com/MunifTanjim/argus/compare/app-0.0.3...app-0.0.4) (2026-06-25)


### Features

* **app:** tweak responsiveness for tablets ([5272939](https://github.com/MunifTanjim/argus/commit/5272939c0a5b68255f235ef960ad25d86e5fdf79))


### Bug Fixes

* **app:** keep welcome screen clear of system navigation bar ([252a75d](https://github.com/MunifTanjim/argus/commit/252a75d4b4e96a3b86970a98a03d9893de8af65d))
* **app:** persist push target and self-heal gateway registration ([af35061](https://github.com/MunifTanjim/argus/commit/af350614bd79868e156965fc67d4123f06f3c889))

## 0.0.3 (2026-06-25)


### Features

* flutter mobile companion app ([26360c6](https://github.com/MunifTanjim/argus/commit/26360c61bf974f01a2d9e564a57bfaffcef68dad))
* update the logo ([2cb7415](https://github.com/MunifTanjim/argus/commit/2cb7415367d8844d1c99db87f4671a61969f0ed9))


### Bug Fixes

* **app:** keep transcript views clear of system navigation bar ([ca4ad36](https://github.com/MunifTanjim/argus/commit/ca4ad3600b5a5b6ff15fb253d31164e059f26e7b))
* **app:** use monochrome status-bar icon from logo ([b527252](https://github.com/MunifTanjim/argus/commit/b5272521c1001f407c6e585138105e064c0e0416))
