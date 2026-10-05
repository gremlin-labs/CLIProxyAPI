# CLI Proxy API

English | [中文](README_CN.md) | [日本語](README_JA.md)

If you want to use CLIProxyAPI on your desktop, we recommend our [EasyCLIProxyAPI](https://github.com/router-for-me/EasyCLIProxyAPI) desktop client. It provides a graphical configuration UI, automatic updates, system tray integration, and one-click start/stop for the CLIProxyAPI service.

CLIProxyAPI is a proxy server that provides OpenAI/Gemini/Claude/Codex/Grok compatible API interfaces for CLI.

You can access the following providers locally and with multiple CLI accounts through any OpenAI (including Responses), Gemini (including Interactions), or Claude-compatible client or SDK.

<table>
<tbody>
    <tr>
        <th align="center" width="100">Provider</th>
        <th align="center">Description</th>
    </tr>
    <tr>
        <td align="center"><a href="https://www.kimi.com/code/?aff=cliproxyapi"><img src="./assets/logo/kimi.svg" alt="Kimi" width="28" height="28" /></a></td>
        <td>Kimi series models (Kimi K3, K2.8 Preview, etc.). <a href="https://platform.kimi.ai/docs/guide/kimi-k3-quickstart">Kimi K3</a> is Moonshot AI’s most capable model and the world’s first open 3T-class model. With 2.8 trillion parameters, native vision, and a 1-million-token context window, K3 is built for long-horizon coding, knowledge work, and reasoning. CLIProxyAPI supports Kimi through OAuth or compatible API interfaces. Try a <strong>Kimi Code plan</strong> (<a href="https://www.kimi.com/code?aff=cliproxyapi">中文站</a> | <a href="https://www.kimi.ai/code?aff=cliproxyapi">Global</a>), or get an <strong>API key</strong> from the Kimi Open Platform (<a href="https://platform.kimi.com?track_id=track-f15622e7182046baa22ca35e006e13a7&aff=cliproxyapi">中文站</a> | <a href="https://platform.kimi.ai?track_id=track-8a28e4b291d84f62af2fccc3e7a21cb3&aff=cliproxyapi">Global</a>). Thanks to Kimi for supporting CLIProxyAPI and the open-source community!</td>
    </tr>
    <tr>
        <td align="center"><a href="https://developers.openai.com/api/docs/models"><img src="./assets/logo/openai.svg" alt="OpenAI" width="28" height="28" /></a></td>
        <td>OpenAI GPT-6 models (GPT-6 Astra, GPT-6.1 Sol, GPT-6 Luna), including access through Codex OAuth. Astra is the flagship for complex reasoning and coding, Sol balances capability and cost, and Luna handles focused, high-volume work.</td>
    </tr>
    <tr>
        <td align="center"><a href="https://www.anthropic.com/claude/fable"><img src="./assets/logo/claude.svg" alt="Anthropic" width="28" height="28" /></a></td>
        <td>Anthropic Claude models (Claude Fable 5.1, Claude Opus 5.5, Claude Sonnet 5.5). Fable 5.1 is built for ambitious, long-running coding and knowledge work; Opus 5.5 brings strong agentic coding at a lower cost.</td>
    </tr>
    <tr>
        <td align="center"><a href="https://antigravity.google/"><img src="./assets/logo/antigravity.svg" alt="Antigravity" width="28" height="28" /></a></td>
        <td>Google Gemini models include Gemini 3.8 Flash and Gemini 3.1 Pro Preview. CLIProxyAPI supports Gemini API, AI Studio, Vertex AI, Gemini CLI, and Antigravity accounts; model availability varies by channel. Gemini 3.8 Flash is Google's latest Flash model for long-horizon software engineering and agentic workflows.</td>
    </tr>
    <tr>
        <td align="center"><a href="https://docs.x.ai/developers/grok-4-7"><img src="./assets/logo/xai.svg" alt="xAI" width="28" height="28" /></a></td>
        <td>xAI Grok models (Grok 4.7, Grok 4.7 Build Fast, etc.). Grok 4.7 is SpaceXAI's latest model for coding, agentic tasks, and knowledge work.</td>
    </tr>
    <tr>
        <td align="center"><a href="https://dev.meta.ai/docs/overview"><img src="./assets/logo/meta.svg" alt="Meta" width="28" height="28" /></a></td>
        <td>Meta Muse models (Muse Spark 1.3, Muse Spark 1.2, etc.). CLIProxyAPI supports Muse Code accounts through Meta login and Meta Model API keys, with Muse Spark 1.3 for coding and agentic workflows.</td>
    </tr>
    <tr>
        <td align="center"><a href="https://devin.ai/cli">Devin</a></td>
        <td>Devin models (SWE-2, GPT-6 Astra, Claude Fable 5.1, etc.). Connect a Devin account with <code>--devin-login</code> to route the models available to that account.</td>
    </tr>
</tbody>
</table>

## New in this fork

This is CLIProxyAPI-GLE (gremlinlabs edition), the gremlinlabs fork of [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI). It tracks upstream and adds the changes below, focused on Claude and OpenAI/Codex accounts.

| Area | Change | Details |
| --- | --- | --- |
| Routing | Reset-aware account selection | New `routing.strategy: reset-aware`. Among accounts in the highest priority tier, the one whose weekly quota window resets soonest is used first, so allowance that would expire unused is spent first. Accounts at or above 95% of their 5-hour or weekly window yield to accounts with headroom, accounts whose included quota is exhausted (where requests would be rejected or billed as overage or credits) are used last, and accounts with no quota data yet get one probe request. Reads the Anthropic and Codex rate-limit headers the proxy already captures. |
| Routing | Sticky thread affinity | With `routing.session-affinity: true` (now on in `config.example.yaml`), a conversation stays on its account to keep its prompt cache warm. A brief outage (transient error, short rate limit, in-request retry) is served by one temporary backup account while the binding stays put, and the thread returns when its account recovers. The binding only moves when the account is disabled, removed, or unavailable for longer than five minutes. |
| Routing | Routing state survives restarts | Quota snapshots and thread bindings are saved to `routing-state.cpa` in the auth directory every minute and on shutdown, and restored at startup. Quota data is tied to the account it was observed for: if a credential is replaced by a different account, its old quota data is dropped rather than steering routing. |
| Routing | Codex limit flags | A Codex `x-codex-limit-reached` (or `x-codex-allowed: false`) response marks the account's included allowance as exhausted. Remaining credits never make it eligible again, so paid overage is avoided. |
| Routing | Encrypted Codex threads stay put | A Codex thread whose input carries encrypted reasoning or compaction items can only be continued by the account that produced them. While that account still exists and is enabled, the thread waits for it (the client gets a rate-limit error with the reset time) instead of moving to an account that would reject it. |
| Routing | Opt-in return to the preferred account | With `routing.session-affinity-return-to-preferred: true`, a thread that failed over to a lower-priority account returns to a higher-priority one once it is available again. Equal priorities never move a thread. Off by default, because each return costs the thread's prompt cache once. |
| Codex | Quota refusals fail over | A usage-limit or `insufficient_quota` refusal that arrives inside an HTTP 200 stream (with `codex.stream-bootstrap-buffering`) now fails over to another account with its quota classification, over both SSE and WebSocket, instead of reaching the client. |
| Codex | WebSocket sessions survive reloads | An auth-file rescan that leaves the config unchanged no longer closes live Codex WebSocket sessions, and a buffered terminal error is delivered before the stream closes. |
| Codex | Oversized WebSocket frames | Requests of 16 MiB or more are refused before upload with the same `message_too_big` / 1009 signal the upstream sends after upload, so the client falls back to POST/SSE straight away. |
| Codex | Deactivated workspaces are disabled | A Codex `402` with `deactivated_workspace` disables the credential (shown in the management panel, kept across restarts) instead of retrying it every 30 minutes. Other `402`s keep the normal cooldown. |
| Claude | Failover on in-stream errors | When Anthropic returns HTTP 200 and then a rate-limit or overload `event: error` before any output, the request fails over to another account instead of passing the error to the client. |
| Panel | CLI PROXY GLE panel | The management panel at `/management.html` is our single golden panel (based on CPAMC++), downloaded from our panel repository's releases. `POST /v0/management/management-html/install` installs the latest release on demand (SHA-256 verified). |
| Observability | Request Monitoring | Per-request usage events, summaries, per-account and per-API-key stats and cost estimates, stored in a local SQLite database (`usage.db` next to the config) with configurable retention. Model prices sync from public price lists (models.dev, LiteLLM, OpenRouter). |
| Models | Context window overrides | Override a model's advertised context window and completion limit (`model-context-overrides`); applied to `/v1/models` and model lookups. |
| Privacy | Request desensitization | Optional (off by default) masking of secrets and personal data in outgoing requests, with placeholders restored in responses. Configurable detectors, custom terms and scope. |
| Codex | Custom instructions and routing options | Optional custom instructions (prepend, append or replace) for chosen Codex models, an opt-in preference for Free-plan credentials on shared models, and opt-in auto-disable rules for repeated auth failures or usage limits. |
| API | Playground | `POST /v0/management/playground/chat` sends a test message through a chosen model, provider and credential. |
| API | Keyless local management | Opt-in `management.local-without-key: true` runs the management API without a key for requests from this machine when no `secret-key` is set. Browser requests are accepted only from localhost pages, so another website open in the browser cannot reach it; remote access still needs a key. Meant for single-user machines. |
| API | Credential delete retries | `DELETE /v8/.../credentials` finishes token-store cleanup when an earlier attempt already removed the file, instead of returning 404. |
| API | Model metadata in `/v1/models` | Each model now includes `context_window` / `max_context_window` and, where the model declares them, `supported_reasoning_levels` / `default_reasoning_level`. Unknown values are omitted rather than guessed. |
| Thinking | Unsupported levels are clamped | On user-defined models, a requested reasoning level the model does not support (for example `xhigh`) is clamped to the nearest supported level instead of being rejected upstream. |
| Reliability | Request size limits | Request bodies and zstd-decompressed bodies are capped at 64 MiB (above the largest provider limits) and oversized requests get HTTP 413, so a small compressed request cannot exhaust memory. |
| Reliability | Connection timeouts | The server times out slow header senders and idle keep-alive connections; streamed responses are unaffected. Dials through a SOCKS5 proxy are now cancelled when the request is. |
| UI | Instrument Sans | The OAuth success pages and the setup warning page use the bundled Instrument Sans font (SIL Open Font License), inlined so the pages make no external requests. |

The web dashboard at `/management.html` is a separate project: [gremlin-labs/Cli-Proxy-API-Management-Center-GLE](https://github.com/gremlin-labs/Cli-Proxy-API-Management-Center-GLE), our fork of the upstream panel with its UI updates. The proxy does not bundle it; on first use and every few hours it downloads `management.html` from the latest release of that repository, checks it against the SHA-256 digest GitHub records for the asset, and serves it locally. Set `management.panel-github-repository` to use a different panel, or `management.disable-auto-update-panel` to stop the periodic update checks.

## Overview

- OpenAI/Gemini/Claude/Grok compatible API endpoints for CLI models
- OpenAI Codex support (GPT models) via OAuth login
- Claude Code support via OAuth login
- Grok Build support via OAuth login
- Streaming, non-streaming, and WebSocket responses where supported
- Function calling/tools support
- Multimodal input support (text and images)
- Multiple accounts with round-robin load balancing (Gemini, OpenAI, Claude, Grok)
- Simple CLI authentication flows (Gemini, OpenAI, Claude, Grok)
- Generative Language API Key support
- AI Studio Build multi-account load balancing
- Claude Code multi-account load balancing
- OpenAI Codex multi-account load balancing
- Grok Build multi-account load balancing
- OpenAI-compatible upstream providers via config (e.g., OpenRouter)
- Reusable Go SDK for embedding the proxy (see `docs/sdk-usage.md`)

## Getting Started

CLIProxyAPI Guides: [https://help.router-for.me/](https://help.router-for.me/)

## Management API

see [MANAGEMENT_API.md](https://help.router-for.me/management/api)

## SDK Docs

- Usage: [docs/sdk-usage.md](docs/sdk-usage.md)
- Advanced (executors & translators): [docs/sdk-advanced.md](docs/sdk-advanced.md)
- Access: [docs/sdk-access.md](docs/sdk-access.md)
- Watcher: [docs/sdk-watcher.md](docs/sdk-watcher.md)
- Custom Provider Example: `examples/custom-provider`

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## Sponsor

For the list of project sponsors, see the [original README](https://github.com/router-for-me/CLIProxyAPI#sponsor).

## Usage Statistics

CLIProxyAPI no longer ships built-in usage statistics. For third-party usage statistics tools, see the [original README](https://github.com/router-for-me/CLIProxyAPI#usage-statistics).

## Who is with us?

For the list of projects built on CLIProxyAPI, see the [original README](https://github.com/router-for-me/CLIProxyAPI#who-is-with-us).

## More choices

For ports of CLIProxyAPI and projects inspired by it, see the [original README](https://github.com/router-for-me/CLIProxyAPI#more-choices).

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
