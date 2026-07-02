# Changelog

## [0.0.6](https://github.com/MunifTanjim/argus/compare/0.0.5...0.0.6) (2026-07-02)


### Bug Fixes

* **tui:** dedupe project cwds in spawn picker ([c3dde7c](https://github.com/MunifTanjim/argus/commit/c3dde7ca0f926eb08583416b9229eac9d36014f0))

## [0.0.5](https://github.com/MunifTanjim/argus/compare/0.0.4...0.0.5) (2026-07-02)


### Features

* add command - config dir ([9bb7cf4](https://github.com/MunifTanjim/argus/commit/9bb7cf4044aa5fadade2283eb1d47a3880ba5f79))
* add global --no-config flag ([7c2b4b8](https://github.com/MunifTanjim/argus/commit/7c2b4b8897bce3ecaba529b7cb4af17c45f669e0))
* add tunnel provider - external ([87972fd](https://github.com/MunifTanjim/argus/commit/87972fdabc14de2add53c896b37888c2fc8d1ac3))
* add tunnel provider - zrok ([ed096ea](https://github.com/MunifTanjim/argus/commit/ed096eac6c77fb319e2781b22d764feaebcd2cb2))
* **app:** support ssh connection ([77813af](https://github.com/MunifTanjim/argus/commit/77813afbc8a03c0a87b38fedb2610eb24eb7ca54))
* log hook event name on rpc request line ([9c042be](https://github.com/MunifTanjim/argus/commit/9c042be7608eeec442f91628b8e59e7b80e02e0c))
* **tui:** add embedded-node logs tab ([9564e9d](https://github.com/MunifTanjim/argus/commit/9564e9dde18c934daaf7a4395b1df243ed64f174))


### Bug Fixes

* detach desktop notify from caller ctx ([e1dd467](https://github.com/MunifTanjim/argus/commit/e1dd4673f01bda931a26db9b4aced2fff264d4dc))
* fire desktop notification from embedded node ([a58b688](https://github.com/MunifTanjim/argus/commit/a58b688e70d398d60557e312a1c2210790e9c437))
* print public url for cloudflare quick tunnel ([54b54a6](https://github.com/MunifTanjim/argus/commit/54b54a63ea890013584588b09f9ef0979de154ab))
* **tui:** scroll a too-tall prompt body in the focused dock ([5e4c0da](https://github.com/MunifTanjim/argus/commit/5e4c0da44b4d1fd60726f13c293f15d399b7a6cd))

## [0.0.4](https://github.com/MunifTanjim/argus/compare/0.0.3...0.0.4) (2026-06-29)


### Features

* add server.info with version and connected nodes ([2977358](https://github.com/MunifTanjim/argus/commit/29773583d86f3b5788068ccf189f642374c1912f))
* add upgrade command ([7f10f5d](https://github.com/MunifTanjim/argus/commit/7f10f5d0bd19f01079b29405151213fd3a4ab207))
* gate spawn-session on per-node tmux availability ([68cfb18](https://github.com/MunifTanjim/argus/commit/68cfb18004e3b7e3370d42e491d4fd99e8fd9671))
* spawn new sessions with a dir picker and initial prompt ([f95e1af](https://github.com/MunifTanjim/argus/commit/f95e1aff41bfebc65019f8d2aeaec7744b3d50b9))


### Bug Fixes

* **push:** recover from gone endpoints by minting a fresh one ([48dfd9b](https://github.com/MunifTanjim/argus/commit/48dfd9b6261c7e188f66e6797233764f09124c65))

## [0.0.3](https://github.com/MunifTanjim/argus/compare/0.0.2...0.0.3) (2026-06-28)


### Features

* add desktop notification for macOS ([e8dbef8](https://github.com/MunifTanjim/argus/commit/e8dbef811f0841a40ac736f21c7aec24062cbcae))
* **gateway:** heartbeat node uplinks to detect half-open links ([f67ea0f](https://github.com/MunifTanjim/argus/commit/f67ea0f81b808fc9eea5bf17918fd52042a0561a))
* support non-tmux claude sessions ([9688398](https://github.com/MunifTanjim/argus/commit/96883981a8873bb9d61aa14087bc82c8d5b25f62))
* **tui:** smarter node startup when you open the dashboard ([5ecd3b9](https://github.com/MunifTanjim/argus/commit/5ecd3b96e0e478ac82f9c306946eec560e5b272a))


### Bug Fixes

* **config:** add fallback when XDG runtime dir is unavailable ([65314c3](https://github.com/MunifTanjim/argus/commit/65314c383d1e907bbe8a81ac0e83152339451ab5))

## [0.0.2](https://github.com/MunifTanjim/argus/compare/0.0.1...0.0.2) (2026-06-25)


### Features

* **adapter/claudecode:** lazy transcript view + ReadSubagentView for nested drilling ([8a32521](https://github.com/MunifTanjim/argus/commit/8a3252162fbf68b31f666116fe0964608f1413c7))
* **adapter/claudecode:** link nested subagents in streaming, capped at depth 5 ([645aed0](https://github.com/MunifTanjim/argus/commit/645aed04df5c90c5957589d4b286344c19e5b996))
* **node:** historyTranscript fetches nested subagent by agent_id ([5a677bc](https://github.com/MunifTanjim/argus/commit/5a677bc7585b5c37d0b1f1b37400058dae79f83a))
* **parser:** nested subagent refs, spawnDepth, single-file read ([8327959](https://github.com/MunifTanjim/argus/commit/832795993504af63d044ad204716e734fbab1a32))
* **tui:** add g/G and ctrl-d/ctrl-u navigation to session and history lists ([0a16bf6](https://github.com/MunifTanjim/argus/commit/0a16bf68a99564fa98dc908b02fff54a094f7010))
* **tui:** drill into nested subagents in history sessions ([e8f2eaf](https://github.com/MunifTanjim/argus/commit/e8f2eaf7ede4984c257f21d1b2ad399c120e1c45))
* **tui:** group history projects by node with per-node headers ([c13a785](https://github.com/MunifTanjim/argus/commit/c13a78599a0be570c04242de1b6f8caed922eaa9))
* **tui:** pre-expand Output items in subagent transcript view ([76e8142](https://github.com/MunifTanjim/argus/commit/76e8142a05776d3d211ff5c2396a6c5f87374a28))
* **tui:** show node on awaiting-input session cards ([129475f](https://github.com/MunifTanjim/argus/commit/129475f8b57fa10574c348e8a37de3ff12dface2))
* update the logo ([2cb7415](https://github.com/MunifTanjim/argus/commit/2cb7415367d8844d1c99db87f4671a61969f0ed9))


### Bug Fixes

* **adapter/claudecode:** read subagent file with sidechain cleared in FindToolDetail ([d02bed1](https://github.com/MunifTanjim/argus/commit/d02bed1c55d17b57d6fc1c798bd8b1da0b8d3a0d))


### Performance Improvements

* make gateway fanout calls concurrent ([d2dde11](https://github.com/MunifTanjim/argus/commit/d2dde110bf08a33b5a1f41ed1f6515d97b29ff82))

## 0.0.1 (2026-06-24)


### Features

* argus CLI ([d51f32e](https://github.com/MunifTanjim/argus/commit/d51f32ea2dcf4ccb22811256adc0203414632711))
* claude code observe adapter ([918d697](https://github.com/MunifTanjim/argus/commit/918d697cb18e0cdf32727b88f936e9e0cb46c536))
* flutter mobile companion app ([26360c6](https://github.com/MunifTanjim/argus/commit/26360c61bf974f01a2d9e564a57bfaffcef68dad))
* remote gateway, tunnels and pairing ([0354f34](https://github.com/MunifTanjim/argus/commit/0354f34f4ca2c41ea30df08104dcef1ab4951cec))
* session registry and discovery core ([f2743e2](https://github.com/MunifTanjim/argus/commit/f2743e227c7f92741b783308d7a6d18a22f30f30))
* terminal UI client ([c95b16c](https://github.com/MunifTanjim/argus/commit/c95b16cf9927d820c7464f9f4a360d9901eceae5))


### Continuous Integration

* add pipeline for release and publish ([7ea8998](https://github.com/MunifTanjim/argus/commit/7ea8998dd3ed8fc44932adbcfdfb1b3d2ab596cf))
