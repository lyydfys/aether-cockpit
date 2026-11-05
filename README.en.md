# Aether Cockpit

**简体中文：** [中文文档](README.md)

A local home for the AI coding channels spread across your CLIs, IDEs, and API settings. Aether Cockpit connects the subscriptions and model APIs you are authorized to use to one gateway, so Claude Code, Codex CLI, Gemini CLI, OpenCode, and other supported clients can reach them through familiar interfaces. Account pools, model routing, failover, quota, and usage are managed in one place.

The request path is **client → local gateway → account and model routing → your configured upstream**. Aether Cockpit handles local access, protocol adaptation, and operations; it does not host upstream accounts or replace your AI client. Credentials, request records, and runtime data stay on your machine by default, and the gateway listens on loopback unless you change it. The Android build runs the same gateway locally, with channel onboarding, quota, and the operations view on the phone.

Compared with pointing each client straight at a vendor, what the gateway adds is **one scheduling layer over several channels**: a client config can target multiple channels, accounts can be pooled and rotated by policy, quota and usage are aggregated in one place, and request logs plus probe results are there when something breaks. All of it runs locally, with no project-operated relay in the middle.

Downloads are published on [GitHub Releases](https://github.com/lyydfys/aether-cockpit/releases/latest); both platforms share the same version string:

| Platform | File | Notes |
|---|---|---|
| Windows x64 | `AetherCockpit-<version>-windows-amd64.zip` | Extract, then run `AetherCockpit.exe` |
| Android arm64 | `AetherCockpit-<version>-android-arm64-v8a.apk` | Signed release build; installing requires allowing "unknown sources" |

See [Quick Start](docs/QUICKSTART.md) and [Features](docs/FEATURES.md) for details.

[![CI](https://github.com/lyydfys/aether-cockpit/actions/workflows/ci.yml/badge.svg)](https://github.com/lyydfys/aether-cockpit/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/lyydfys/aether-cockpit)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-Windows%20x64%20%7C%20Android%20arm64-2675bf)](#requirements)

## Table of Contents

- [One entry point for your AI channels](#one-entry-point-for-your-ai-channels)
- [Interface preview](#interface-preview)
- [Channel coverage](#channel-coverage)
- [Get Started](#get-started)
- [What It Does](#what-it-does)
- [Requirements](#requirements)
- [Build from Source](#build-from-source)
- [Security and Data](#security-and-data)
- [FAQ](#faq)
- [Documentation](#documentation)
- [Contributing](#contributing)
- [Design references and credits](#design-references-and-credits)
- [License](#license)

## One entry point for your AI channels

- **Each client keeps its strengths:** supported AI coding clients share the local gateway; pick OpenAI Chat, Responses, Anthropic Messages or the Gemini API depending on the client.
- **Several accounts working together:** pool accounts from different channels, then distribute requests through routing groups with session stickiness, rotation or quota-aware strategies, and fail over when possible.
- **Runtime state in one place:** inspect model lists, channel quotas, token and request usage, cost, probe results and request logs together.
- **Surrounding workflows too:** manage client connection configs, prompts, skills, MCP servers, subagents and standalone client sessions.

## Interface preview

The screenshots below come from an isolated demo configuration; accounts, quotas and usage are synthetic data.

**Overview**: six example channels feeding one API endpoint, with account, quota, call and probe state on a single page.

![Aether Cockpit overview with six example channels connected to shared local API endpoints](docs/images/screenshots/overview-en.png)

**Settings**: listen address, LAN sharing, access control and redaction options for the local gateway. Only machine-local options appear; no personal credentials are shown.

![Local appearance, network, and probe settings](docs/images/screenshots/settings-en.png)

**Usage**: requests, tokens and cost aggregated by time and channel, computed from local records.

![Synthetic usage trend and breakdown](docs/images/screenshots/usage-en.png)

## Channel Coverage

The channel catalog includes subscription integrations as well as API platforms and compatible endpoints. These are representative examples; login methods, quota visibility, and model capabilities vary by provider and account. The running application shows the current catalog.

| Type | Examples |
| --- | --- |
| Domestic subscriptions and coding services | Qoder, Trae, WorkBuddy, CodeBuddy, ZCode, MiniMax |
| International subscriptions and client accounts | Claude, Codex, GitHub Copilot, Cursor, Antigravity, Kiro, Devin, OpenCode |
| APIs and cloud platforms | OpenAI-compatible endpoints, Azure OpenAI, Gemini, Vertex AI, Amazon Bedrock |

![Subscription and API channel categories in the catalog](docs/images/screenshots/channels-zh.png)

*The catalog is broader than the six demo channels in the overview image. It lists integrable types; the image's account, quota, request, and probe values are synthetic and do not indicate live account state.*

## Get Started

### Windows desktop

1. Download `AetherCockpit-<version>-windows-amd64.zip` and extract it to a folder (do not run it from inside the archive).
2. Double-click `AetherCockpit.exe`. The desktop build needs the Microsoft Edge WebView2 Runtime, which Windows 10/11 normally ships with.
3. The first launch listens on `127.0.0.1:7860`; open <http://127.0.0.1:7860/> in a browser.
4. For headless setups use `aether-cockpit-cli.exe` from the same archive — no GUI required.

**Data directory**: created on first launch under your user profile (`%LOCALAPPDATA%\AetherCockpit` on Windows) and contains `settings.json`, `accounts.json`, `state.db`, `logs/aether.log` and `usage/`. Move the whole folder when backing up or migrating; it holds account data, so never commit it or paste it into a chat.

**Default port**: `7860`. If it is taken, change the listen port in Settings, or pass a startup argument (see the FAQ).

### Android

1. Download `AetherCockpit-<version>-android-arm64-v8a.apk`.
2. Android will warn about "unknown sources"; allow that source before continuing — this is normal for sideloaded APKs.
3. **arm64-v8a** devices only, **Android 8.0 (API 26)** or newer.
4. Start the local gateway inside the app; channel onboarding and the overview then work on the phone. Windows-only capabilities are labelled "Windows only" in the UI.

### Connect a model

1. Add a subscription account or API service you are authorized to use under "Channels".
2. Run a probe under "Gateway & Probe" and copy the connection config for your target client.
3. Use `channel-id/model-name` as the model name, e.g. `openai-compat/gpt-4.1`; the list from the UI or `/v1/models` is authoritative.

You can also check routable models from any compatible client or shell. Once a gateway key is configured, replace `YOUR_GATEWAY_KEY` with it, and replace the model name with one returned by `/v1/models`.

```powershell
curl.exe http://127.0.0.1:7860/v1/models `
  -H "Authorization: Bearer YOUR_GATEWAY_KEY"

curl.exe http://127.0.0.1:7860/v1/chat/completions `
  -H "Authorization: Bearer YOUR_GATEWAY_KEY" `
  -H "Content-Type: application/json" `
  -d '{"model":"<provider-id>/<model-id>","messages":[{"role":"user","content":"Hello"}]}'
```

Client configs may contain the gateway key. Copy it from the UI and keep it local; never publish a real gateway key, provider credential, account data, or unredacted logs in an issue or screenshot.

## What It Does

- Accepts OpenAI Chat, OpenAI Responses, Anthropic Messages, and Gemini API requests, including supported streaming and tool-call flows.
- Pools provider accounts with sticky sessions, routing groups, quota-aware strategies, and failover.
- Tracks quota, usage, costs, request logs, scheduled tasks, and provider health.
- Manages supported client configuration, prompts, Skills, MCP servers, sub-agents, and isolated client sessions.
- Keeps account credentials and runtime data in the local data directory by default, with request privacy filters and redacted exports.

See [Features and Compatibility](docs/FEATURES.md) for details and limitations.

**Boundaries**: the following exist only in the Windows desktop build and are labelled "Windows only" on Android, together with the reason — WSL distribution probing, taking over Windows client processes, local client scanning, and PC-isolated instances. Update checking only checks, notifies and opens the Release page; it never downloads or silently installs.

## Requirements

| Target | Requirement |
| --- | --- |
| Desktop (prebuilt) | Windows 10/11 x64; Microsoft Edge WebView2 Runtime |
| Headless (prebuilt) | Windows x64 in current releases; build from source for other platforms and verify yourself |
| Android (prebuilt) | arm64-v8a device; Android 8.0+ (API 26); "unknown sources" allowed |
| Build from source | Go 1.26+, Node.js 20+ (frontend checks only), JDK 17+ (JDK 21 used for current builds), Android SDK 36 + NDK 29.0.13113456 (Android only) |

## Build from Source

Prerequisites: Go 1.26 or newer; Node.js 20 or newer (frontend checks only); JDK 17 or newer (JDK 21 for the current Android build); Android SDK 36 and NDK 29.0.13113456 when building the Android app — point `mobile/android/local.properties` or `ANDROID_HOME` at them.

```powershell
# 1) Desktop (Windows)
go run ./cmd/winres-icon
go build -tags desktop,production -ldflags "-H windowsgui" -o AetherCockpit.exe ./cmd/desktop

# 2) Android (arm64-v8a)
bash scripts/build-android-mobilebridge.sh arm64-v8a   # builds the Go-side .so
cd mobile/android && gradlew.bat :app:assembleDebug    # or :app:assembleRelease (needs signing)

# 3) Tests
go test ./...
cd mobile/android && gradlew.bat :app:testDebugUnitTest
```

Headless/CLI build: `go build -o cockpit.exe ./cmd/cockpit`. Frontend checks: `npm ci && npm run test:ui && npm run typecheck && npm run ui:lint`.

`scripts/gate.sh` is the maintainers' one-shot gate: it checks free disk space first (a full disk makes the linker report "not enough space", which looks like a test failure), runs `go vet`, rebuilds artifacts (otherwise "all green" only proves the sources compile while `bin/` is stale), runs the full test suite serially (parallel runs occasionally hit resource-related ".test build failed"), then runs the mock smoke test and verifies artifact hashes. `--quick` skips the smoke step, `--no-build` only verifies sources, `SKIP_TESTS=1` runs disk/vet/rebuild/smoke/artifacts only.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full development gate. Real-provider smoke tests can contact upstream services and consume account quota; use the documented mock flow for routine development.

## Security and Data

Aether Cockpit is a local gateway, not a hosted proxy.

- **Where credentials live:** account credentials, cookies, OAuth tokens and proxy passwords are written into the local data directory (`accounts.json`, `state.db`, and the platform keystore) behind the project's own encryption layer. They are never committed to the repository and never uploaded to a server of ours.
- **What is uploaded:** apart from the upstream channels you configure, nothing is sent to third parties — no accounts, prompts, or usage. Update checks only query GitHub Releases' public API and can be switched off.
- **Listen scope:** loopback `127.0.0.1` only by default. Enabling LAN sharing exposes the gateway to the same subnet; the management API still requires the in-app session credential, so opening the page is not enough to change settings.
- **Log redaction:** request logs record metadata by default (time, model, status, latency, tokens). The built-in redaction engine replaces credentials and personal data with placeholders by prefix or regex, and redacted records can be exported. Full request bodies are not persisted.
- **Data directory:** contains local accounts and runtime data — do not share or commit it. Local acceptance screenshots under `docs/evidence/` are likewise unversioned.

Before enabling LAN or wider network access, set a gateway key and review allowed sources. Follow the terms of each provider you connect; the project does not supply accounts or authorization and cannot guarantee upstream availability or quota. See [PRIVACY.md](PRIVACY.md) for data flows and [SECURITY.md](SECURITY.md) for vulnerability reporting.

## FAQ

**Port 7860 is already in use — what now?**
Change the listen port in Settings (for example to 7861) and restart the gateway, or pass a startup argument. Check the occupant with `netstat -ano | findstr :7860` on Windows or `lsof -i :7860` on macOS/Linux. Remember to update client base URLs after changing the port.

**LAN sharing is on but another device still cannot connect.**
Check three things in order: ① both devices are on the same subnet (different VLANs, guest networks and some routers' "client isolation" block it); ② the host firewall allows the port (Windows prompts on first listen — allow the private network); ③ the management API requires the in-app session credential, so a browser on another device is rejected by design — what you can reach is the gateway API, and it needs the gateway key you configured.

**The update check returns 5xx or times out — is the app broken?**
No. Update checks query GitHub's public API, which returns 5xx or times out in some network environments; the UI shows "check failed" and nothing else is affected. Retry later or download from the Releases page directly.

**The APK will not install — "unknown sources" or "unsafe".**
Sideloaded APKs require allowing that source (Settings → Apps → Special access → Install unknown apps → pick your file manager or browser). Also confirm the device is **arm64-v8a** and Android 8.0+; x86 emulators and 32-bit devices are out of scope.

**Do I need to sign in again after updating?**
No, not for a normal over-the-top install: credentials live encrypted in the local data directory. You only need to re-authorize after uninstalling/reinstalling, changing devices, or clearing the data directory.

**Why are some desktop capabilities missing on the phone?**
WSL probing, taking over Windows client processes, local client scanning and PC-isolated instances depend on a Windows host and have no equivalent on Android. The UI labels them "Windows only" and explains why, instead of shipping a button that does nothing.

## Documentation

- [Documentation index](docs/README.md)
- [Privacy](PRIVACY.md)
- [Quick start](docs/QUICKSTART.md)
- [Features and compatibility](docs/FEATURES.md)
- [Adding a provider](docs/ADD_PROVIDER.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Changelog](CHANGELOG.md)

## Contributing

Bug reports, focused feature requests, and pull requests are welcome. Please read the [contribution guide](CONTRIBUTING.md) and [Code of Conduct](CODE_OF_CONDUCT.md) first. For general questions, see [SUPPORT.md](SUPPORT.md).

Before opening a PR, run these gates locally — they cover both the desktop and Android lines:

```bash
go test ./...
cd mobile/android && gradlew.bat :app:testDebugUnitTest   # mobile unit tests
bash scripts/check-mobile-theme-contract.sh               # theme contract: no hardcoded colors/sizes, no bespoke theme
bash scripts/check-no-external-project-names.sh           # zero external project names in the repo
```

Also run `scripts/gate.sh` when touching native libraries or the release flow. CI repeats the same checks, so running them first saves a round trip.

## Design references and credits

The channel catalog, routing strategies, and usage views were informed by [magpie](https://github.com/yetone/magpie) (MIT) and [sub2api](https://github.com/Wei-Shaw/sub2api) (LGPL-3.0). Thanks to their authors and contributors. No code from those projects is copied into or linked against this repository; attribution and license details are listed in [NOTICE](NOTICE).

## License

Apache License 2.0. Third-party attributions are listed in [NOTICE](NOTICE).
