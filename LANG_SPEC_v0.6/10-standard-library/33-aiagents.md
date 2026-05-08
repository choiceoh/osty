### 10.33 AI Agents (`std.aiagents`)

`std.aiagents` contains dependency-light primitives for building agent
applications. It deliberately starts with the parts that can be implemented as
pure Osty data shapes and helper logic: message/session models, tool presets,
trust boundaries, and compaction policy.

The module does not define a model provider, transport, database, Telegram
client, RPC server, or filesystem workflow. Those belong in host runtimes or
application packages. The stdlib surface gives those packages a stable common
vocabulary.

> **v0.6 capability layering**: `std.aiagents` is *entirely pure* —
> the chapter never touches `Net`, `Fs`, `Process`, or `Clock`. The
> agent run surface (`runRequestWithOptions`, `RunResult` shapes,
> `toolPolicy`) returns / consumes already-captured values. Hosts
> drive the actual model call through `std.ai` (§10.36, `Net`
> required), persist sessions through their own `Fs` capability,
> and source timestamps through `Clock`. The `TrustLevel` enum
> (`Trusted` / `UserProvided` / `Extracted` / `ToolOutput` /
> `CompactionSummary`) is the agent-side analog of the §21 flow
> tag set — `UserProvided` content carries the same suspicion as
> `#[taint("user_input")]` and must pass through a sanitizer (or
> a stricter `SafetyPolicy` check) before being routed into a
> sensitive tool call.

```osty
use std.aiagents

let opts = aiagents.denebChatOptions()
let req = aiagents.runRequestWithOptions("telegram:42", "summarize this", opts)
let policy = aiagents.toolPolicy(aiagents.DenebChat)
let _ = aiagents.ensureToolAllowed(policy, "web")?
```

#### Core Shapes

- `Role` — `System`, `User`, `Assistant`, `Tool`
- `TrustLevel` — `Trusted`, `UserProvided`, `Extracted`, `ToolOutput`,
  `CompactionSummary`
- `ContentKind` — `Text`, `Image`, `Audio`, `Video`, `Document`, `Link`,
  `ToolResult`
- `ContentBlock` — typed text/media/tool-result block plus source/trust
  metadata
- `Message` — role plus a list of content blocks
- `Session` — key, kind, label, message list, timestamps, and active tool
  preset
- `RunOptions`, `RunRequest`, `RunResult`, `RunStatus` — common run envelope

#### Tool Presets

`ToolPreset` standardizes named allow-lists:

- `AllTools` — unrestricted host-defined tools
- `Conversation` — read/web/wiki/fetch-tools style chat mode
- `DenebChat` — web-only assistant mode, with media/link extraction expected to
  happen before the run
- `Boot` — minimal persistent key-value startup mode
- `Researcher`, `Implementer`, `Verifier` — role presets for subagents

`toolPolicy`, `allowedTools`, `knownToolPresets`, `parseToolPreset`, and
`ensureToolAllowed` provide the pure validation layer. Host runtimes are still
responsible for actually filtering tool schemas and rejecting disallowed calls.

#### Safety And Compaction

`SafetyPolicy` records the baseline trust boundary:

- user input and attachments are untrusted by default
- hidden prompts, compaction memory, secrets, and routing details stay private
- the latest user message wins over older memory summaries when they conflict

`CompactPolicy` mirrors the practical thresholds used by dependency-free agent
runtimes:

- soft compaction at 70 percent of budget
- hard compaction at 85 percent of budget
- previous-turn tool results compact to 4096 characters
- emergency input threshold at 30000 estimated tokens

`shouldCompact`, `estimateTextTokens`, `estimateMessagesTokens`,
`shouldCompactToolResult`, `truncateHeadTail`, `rankLines`, `scoreOutputLine`,
and `isPanicAnchor` are pure helpers that let host runtimes make compaction
decisions without pulling in a model or storage engine.

`truncateHeadTail` preserves the beginning and end of long content and replaces
the middle with a truncation marker. `rankLines` is the smarter path for logs,
test output, and tool results: it scores panic/fatal/error/warning lines,
expands nearby context around high-scoring anchors, then reassembles selected
lines with omission markers.

#### Trust-Boundary Helpers

`denebChatSystemPrompt` and `denebChatOptions` provide a compact private-chat
default. `neutralizeSystemTags` removes or downgrades common injected system
markers such as `<system-reminder>`, `[System Message]`, and line-prefixed
`System:` labels.
