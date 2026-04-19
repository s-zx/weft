# 复现 Warp (AI Native Terminal) 技术方案

> 本文档原为 fork 上游 `wavetermdev/waveterm` 的 ROADMAP.md，已替换为 crest fork 的实际方案。
>
> 状态图例：✅ Done | 🔧 In Progress | 🔷 Planned | 🤞 Stretch Goal

## Context

目标是复现 Warp —— 一个把 agent 当一等公民的终端。经 Warp 官网功能页 + 需求澄清确认,Warp 当前定位是 **agentic terminal**:agent 是终端**内置**的(不是 wrap 外部 claude/cursor CLI),所有 AI 能力都是终端本体直接调用 LLM API 实现。核心 UX 流水线是 `Prompt → Agent 执行 → 代码 Review → Re-prompt → Ship`。

候选基座对比:
- **wavetermdev/waveterm** (Electron + Go, Apache-2.0, 跨平台, block UI 已有, 19.7k stars) — **选为基座**
- **manaflow-ai/cmux** (Swift/AppKit + libghostty, macOS-only, GPL-3.0) — License 不友好 + 仅 macOS,不做代码基座;但"多 agent 并行 + 工作空间隔离"设计借鉴

---

## 最终 MVP 范围(经需求澄清)

### ✅ 必做 (P0)

| 类别 | 功能 |
|---|---|
| **终端 UX** | Command Blocks + 块间导航、IDE 风输入编辑器、垂直 tab + git 元数据(branch/worktree/PR) |
| **内置 Agent** | 多步 tool-use 循环 + Thought 流式展示、Prompt 引用文件/目录、Prompt 引用图片、追问 + 会话历史 |
| **代码交互** | ✅ 内置文件树、内置文件编辑器 + LSP（待）、✅ 本地 git diff Code Review 面板、Interactive Code Review 逐行评论 → send to agent（待）、Agent 修改 diff 预览确认（待） |
| **并发** | 多 Agent 并行(cmux 风格,git worktree 隔离) |

### ❌ 明确不做 (out of scope)

主题/外观定制(v1 后再做)、Knowledge Store、Warp Drive 团队共享、Workflows 参数化模板、Session live share、语音输入(Wispr Flow)。

---

## Agent 实现路径:混合策略

**Phase A (MVP)** — 用 `@anthropic-ai/claude-agent-sdk`(Node)起步
- 开箱即用的 agent loop、shell tool、file tool、MCP 支持
- 2-3 周跑通 demo,专注打磨 UX(Blocks、diff review、file tree)
- 限制:默认绑定 Claude 模型,切 provider 需改造

**Phase B (v1 后)** — 渐进替换成自研 agent loop
- 抽象 `LLMProvider` 接口,适配 Anthropic/OpenAI/Gemini/Ollama
- 自实现 tool-use 循环 + prompt caching + streaming
- Claude Agent SDK 的 tool 实现作为参考,不复制代码

这条路线前期快速验证 UX,后期保留多 provider 与自定义 agent 行为的扩展空间。

---

## 目标架构

```
┌─────────────────────────────────────────────────────────────────┐
│ Electron 主进程 (Node)                                           │
│   - 窗口/IPC/菜单/自动更新                                       │
│   - BYOK 密钥安全存储 (safeStorage: keychain/DPAPI)              │
│   - Claude Agent SDK 宿主进程 (MVP) / 自研 LLM 客户端 (v1+)     │
├─────────────────────────────────────────────────────────────────┤
│ Renderer (React + TS)                                           │
│  ┌──────────┬──────────┬──────────────────┬─────────────────┐  │
│  │ Vertical │ File     │ Block Grid       │ Agent Panel     │  │
│  │ Tabs     │ Tree     │ (xterm.js blocks │ (thoughts/tools │  │
│  │ (git     │          │ + inline diff    │ /follow-up)     │  │
│  │ meta)    │          │ + code review)   │                 │  │
│  └──────────┴──────────┴──────────────────┴─────────────────┘  │
│                        ↕                                        │
│  IDE 输入框 (CodeMirror 6 + bash AST + @-file/@-image 补全)    │
├─────────────────────────────────────────────────────────────────┤
│ Go 后端 (wavesrv)                                               │
│   - PTY 管理 (creack/pty, Windows ConPTY)                       │
│   - Shell 集成 (OSC 133/633 边界解析)                           │
│   - Block 存储 (SQLite)                                         │
│   - Worktree 管理 (多 agent 并行隔离)                           │
│   - File System APIs (tree/read/write, 供 agent 与 UI 共用)     │
│   - LSP 代理 (tsserver/rust-analyzer/gopls/pyright 本地启动)    │
│   - Agent SDK 桥 (IPC: Node ↔ Go)                               │
└─────────────────────────────────────────────────────────────────┘
```

**关键集成点**:
- **Agent 与 Shell 共享状态**:agent 的 `run_command` tool 通过 Go 后端的同一 PTY 抽象执行,输出自动成为新 Block,用户和 agent 看到完全一致的历史。
- **Agent 与编辑器共享状态**:agent 的 `edit_file` 通过 Go 后端写盘后,前端文件树自动刷新,内置编辑器高亮变更。
- **Code Review 流水线**:agent 编辑完成后,前端用 git diff 生成视图,用户逐行评论 → 评论打包成 follow-up prompt → send to agent。

---

## MVP 实现拆解(11 个功能模块)

### 1. Shell 集成 + Command Blocks
- 注入脚本:bash/zsh/fish 的 `~/.config/<app>/shell-integration/*.sh`,发 OSC 133 `A/B/C/D` 边界。
- 兼容 starship / oh-my-zsh / powerlevel10k(常见 prompt 框架)。
- Go 侧 `pkg/shellexec/` 新增 OSC 133 解析器,把 PTY 流切成 `{cmd, cwd, exit_code, duration, output, ts}`。
- 前端 block 组件显示 exit_code 徽章、duration、cwd、git branch。
- 块间导航:`⌘↑/↓` 跳块,`⌘K` 命令面板,`⌘F` 块内搜索。

### 2. IDE 风输入编辑器
- **CodeMirror 6**(首选)或 Monaco,选 CodeMirror 因包体小且 Electron 性能好。
- 语法高亮:`@lezer/shell` 或 `tree-sitter-bash` wasm。
- 多光标 + 括号匹配 + 自动缩进。
- `@` 触发文件/目录/图片补全(见模块 5)。
- 高度自适应,长 prompt 可滚动。

### 3. 垂直 Tab + Git 元数据
- 左侧垂直 tab 栏,每个 tab 一个 PTY 会话。
- Tab 元数据:`cwd`、git branch、worktree 状态、是否有 agent 运行中。
- Go 后端定期 `git status --porcelain -b` 采集,避免频繁 fork。
- tab 标题可编辑,失焦恢复自动生成(基于 cwd + 最近命令)。

### 4. 内置 Agent Loop(Claude Agent SDK 集成)
- 主进程引入 `@anthropic-ai/claude-agent-sdk`。
- 定义 tools(覆盖 SDK 默认 + 追加):`run_command`(走 Go PTY)、`read_file`、`edit_file`(diff 预览)、`list_dir`、`grep`、`open_in_editor`。
- 系统 prompt 注入:当前 cwd、shell、OS、最近 5 个 block、活跃文件。
- 流式输出:SDK 的 `query()` async iterator 把每个事件推给 renderer:
  - `thought` → 显示 "Thought for N seconds"
  - `tool_use` → 绿色边框 block(pending → success/error)
  - `text` → markdown 块
- 触发:输入框 `⌘I` 切换到 agent 模式,或 `#` 前缀。

### 5. Prompt 引用文件/目录/图片
- 输入 `@` 弹出补全:文件路径(模糊匹配)、目录、"当前 block 输出"、"git diff" 等结构化上下文。
- 选中后 UI 渲染为 chip(`@src/app.ts`),点击可预览。
- 图片:拖入 / `⌘V` 粘贴,base64 存储,调用时走 Claude vision 的 `image` content block。
- SDK 调用前把所有 `@` 引用展开为 `tool_result` 或 content block 注入 message。

### 6. 追问 + 会话历史
- 每个 agent 会话是一个 "session block",包含多轮 user/agent turn。
- "Ask a follow up" 保留 SDK session id(或自管 message list)续对话。
- 历史落 SQLite,tab 关闭后可通过"最近会话"恢复(不复活 PTY,只恢复对话)。

### 7. 内置文件树
- 侧栏文件树组件,基于 `cwd` 或选定项目根。
- 懒加载,忽略 `.gitignore` + `.git/`。
- 右键菜单:添加到 prompt(`@path` 注入输入框)、在编辑器打开、复制路径、新建文件。
- Agent 修改文件时实时高亮(SSE 订阅 Go 后端 fsnotify 事件)。

### 8. 内置文件编辑器 + LSP
- 复用模块 2 的 CodeMirror 实例(或独立一份,按场景切主题)。
- LSP 代理:Go 后端进程管理器启停 tsserver/rust-analyzer/gopls/pyright,stdio 协议通过 WebSocket 暴露给前端。
- 前端用 `@codemirror/lsp-client` 或 `codemirror-languageserver` 包连接。
- 支持:补全、悬停、诊断、跳转定义、格式化。
- 文件变更未保存时 tab 上红点,`⌘S` 落盘。

### 9. Interactive Code Review(重点差异化)
- Agent 编辑后,后端用 `git diff HEAD -- <files>` 或内存 diff 生成 patch。
- 前端渲染双栏 diff 视图(`react-diff-viewer-continued` 或自写)。
- 交互:点击行号 → 内联评论框 → 多条评论累积 → "Send to Agent" 打包成 follow-up prompt。
- Prompt 结构:`{file_path, line, comment}[]` + 当前 diff + 原始用户请求上下文 → 触发 agent 新一轮 tool-use。
- 支持 "Approve all" / "Discard all" 一键操作。

### 10. Agent 修改 Diff 预览确认
- `edit_file` tool 不直接落盘,先产生 pending diff。
- 前端弹 diff 卡片:`+X -Y` 数字 + 预览 + `Approve` / `Reject` / `Edit` 按钮。
- `Approve` 才真正写文件(走 Go `pkg/fs/`);`Reject` 反馈给 agent("user rejected this change, reason: ...")继续循环。
- 配置项:某些目录/文件类型自动批准(低风险),默认全部人工确认。

### 11. 多 Agent 并行(cmux 风格)
- 启动新 agent 时可选 "在 worktree 运行"。
- Go 后端 `pkg/worktree/`:`git worktree add .worktrees/<agent-id> <base-branch>`。
- 新建一个 vertical tab 绑定这个 worktree,PTY cwd 指过去。
- 完成/失败时桌面通知(Electron `Notification` API) + tab 红点。
- 完成后引导流程:review diff → merge back(自动 `git merge --squash` 或生成 PR)。
- 限制:同时最多 N 个(默认 3,可配置),避免资源打爆。

---

## 关键技术决策

### A. Shell 集成协议
**OSC 133** (Final Term) 为主,附带 **OSC 633** (VS Code) 兼容。降级检测:若 30 秒内未收到边界序列,提示用户 source 脚本。注入脚本可选项:自动写入 `.bashrc/.zshrc`(需用户授权) vs 每会话 `exec` 临时 source。推荐后者,避免污染用户配置。

> **实际选择**:用 waveterm 已有的 **OSC 16162**(比 OSC 133 更丰富,带 JSON 元数据;bash/zsh/fish/pwsh 注入脚本完备),不再走 OSC 133。

### B. Agent SDK 的局限与备用方案
- Claude Agent SDK 当前**只支持 Claude 模型**,若 MVP 要多 provider 得绕路(自写 tool bridge 把 SDK 的 tool 定义翻译给 OpenAI function calling)。
- SDK 的 session 状态管理/ cache 内置,但扩展自定义 tool 需遵循其 MCP 协议。
- **备选**:若 SDK 不够灵活,退回到用 Anthropic SDK 手写 tool-use 循环,预计多花 1-2 周。

### C. LSP 性能
- 每个 tab 一个 LSP 实例太重,改成 **workspace-scoped**:按 git 根目录共享 tsserver,跨 tab 复用。
- 大仓库(monorepo)初始索引慢,显示 "Indexing..." 骨架屏。
- 内存上限:LSP 进程 >= 2GB 时自动重启。

### D. Block 渲染性能
- xterm.js + WebGL renderer 处理单 block。
- 长输出 block(日志 tail)虚拟化:仅渲染可视区域 + scrollback buffer。
- 超过 10 万行自动折叠,显示 "Show more"。

### E. License
- Fork waveterm(Apache-2.0),保留 NOTICE 与版权声明即可商用。
- **绝不**复制 cmux 代码(GPL-3.0 传染性)。
- 自研部分建议 MIT。
- 依赖白名单:核心依赖检查 license,拒绝 AGPL / SSPL / 不明确条款。

### F. BYOK 与数据合规
- API key 通过 Electron `safeStorage` (macOS keychain / Windows DPAPI / Linux libsecret) 加密落盘。
- 用户可配 "本地模型优先"(Ollama),默认关闭遥测。
- Agent 对话/diff 不上传任何服务器(MVP 阶段零后端),所有持久化都在本地 SQLite。

---

## 关键待改造文件(fork waveterm 后核对)

预估路径(基于 waveterm 典型布局):

| 路径 | 改动类型 | 内容 |
|---|---|---|
| `frontend/app/` | 扩展 | 新增 Agent Panel、File Tree、Code Review、Diff Preview 组件 |
| `frontend/app/block/` | 扩展 | Block 增加 exit_code/duration/git_branch 徽章;agent session block 类型 |
| `frontend/app/term/` | 扩展 | xterm.js OSC 133 hook,块边界事件上报 |
| `frontend/app/input/` | 重写 | 输入框换 CodeMirror 6,加 `@`-补全 |
| `frontend/app/editor/` | 新增 | 文件编辑器组件,接 LSP WebSocket |
| `pkg/shellexec/` | 扩展 | OSC 133 解析器 + shell 集成脚本资源 |
| `pkg/wshrpc/` | 扩展 | RPC 方法:`agent/query`、`agent/follow_up`、`fs/tree`、`fs/read`、`fs/write_pending`、`lsp/start` |
| `pkg/fs/` | 新增/扩展 | 文件树、读写、pending diff、fsnotify 订阅 |
| `pkg/lsp/` | 新增 | LSP 进程管理、stdio↔WebSocket 桥 |
| `pkg/worktree/` | 新增 | git worktree 生命周期 |
| `pkg/wstore/` | 扩展 | schema 加 `agent_sessions`、`blocks` 表 |
| `electron/main/agent/` | 新增 | Claude Agent SDK 集成,IPC bridge |
| `electron/main/keychain.ts` | 新增 | BYOK 密钥加密存储 |

---

## 里程碑 & 工期(单人全职)

| 状态 | 阶段 | 周数 | 交付 |
|---|---|---|---|
| ✅ | **M0** Fork + 熟悉 waveterm 代码 | 2 | 本地 `task dev` 跑通,完全理解 block/pty 模型 |
| 🔧 | **M1** Shell 集成 + Block 增强 + 垂直 tab 元数据 | 3 | 模块 1、3,bash/zsh/fish 兼容,git 徽章 |
| 🔷 | **M2** IDE 输入编辑器 + Agent SDK 接入 + 追问 | 3 | 模块 2、4、6,⌘I 唤起 agent,流式 thought 展示 |
| 🔷 | **M3** File Tree + `@`-补全 + 图片 prompt | 2 | 模块 5、7 |
| 🔷 | **M4** 编辑器 + LSP + Diff 预览 | 3 | 模块 8、10 |
| 🔷 | **M5** Interactive Code Review | 2 | 模块 9 |
| 🔷 | **M6** 多 Agent 并行 + Worktree | 2 | 模块 11 |
| 🔷 | **M7** 打磨 + 三平台打包 + 签名 | 2 | macOS/Linux/Windows 安装包,公测版 |
| | **合计** | **19 周 ≈ 4.5 个月** | v1 beta |

**团队 2-3 人**:前后端并行可压缩到 ~2.5-3 个月。

---

## 关键风险

1. **Shell 集成兼容性** — oh-my-zsh / starship / powerlevel10k 的 prompt 钩子可能覆盖 OSC 序列。缓解:准备 shell 模式检测 + 主动发起 ping 测试 + 降级到基于空行启发式的块切分。
2. **LLM 成本失控** — agent 多步循环每次都带大 context 容易爆费。缓解:必开 prompt caching、限制单 session 最大轮次、提供"本地 Ollama"降级路径。
3. **Claude Agent SDK 绑定风险** — SDK API 变化或 license 调整影响。缓解:Phase B 自研路线保留,所有 tool 实现抽象成 interface,SDK 只是一种实现。
4. **LSP 稳定性** — tsserver 对大 monorepo 极吃内存。缓解:进程监控 + 自动重启 + OOM 降级到仅语法高亮。
5. **Electron 打包签名** — Apple Notarization / Windows SmartScreen 流程复杂。缓解:尽早跑通签名流水线,M1 就做一次。
6. **法务边界** — 不复制 Warp 私有 UI 资产/图标;Workflows 官方仓库本就 MIT 可用(若后期要做)。

---

## MVP 验证清单

端到端跑通下列场景即视为 MVP 完成:

1. **Block** — bash/zsh/fish 下执行 `ls && false`,UI 显示独立 block,exit_code 红色徽章。
2. **IDE 输入** — 输入 100 行 prompt,多光标选择,语法高亮正常。
3. **垂直 tab** — 切换不同 cwd 的 tab,tab 上显示对应 git branch/worktree。
4. **Agent 基础** — ⌘I "帮我修 src/app.ts 的 lint 错误",看到 agent 读文件 → grep → edit → 完成流程,每步 Thought/Tool 实时显示。
5. **@-引用文件** — prompt 里 `@src/app.ts` 自动 attach,agent 收到后能读取。
6. **@-引用图片** — 粘贴截图 prompt "把这个 UI 用 React 实现",agent 看懂。
7. **追问** — agent 完成后问 "加一下单测",上下文保持。
8. **文件树 + 编辑器 + LSP** — 点开 `src/app.ts`,有补全/诊断/跳转。
9. **Diff 预览** — agent 改文件前弹 diff,Reject 后 agent 重试,Approve 落盘。
10. **Code Review** — agent 提交一批改动,左侧 diff 视图,第 10 行评论"这里用 const 而非 let",Send to Agent,agent 修正后重新 review。
11. **多 Agent 并行** — 同时启动 2 个 agent(独立 worktree),互不干扰,完成通知。
12. **跨平台** — macOS / Ubuntu / Windows 11 上模块 1-11 全部通过。

测试矩阵:bash 5.x / zsh 5.9 + oh-my-zsh / fish 3.7 / PowerShell 7。LSP 覆盖:ts/js/rust/go/python。

---

## 起步:Fork 到自己的仓库

**不是直接 clone,而是先 fork**,这样有自己的 remote 可以推,还能通过 upstream 定期拉 waveterm 的安全修复/新功能。

```bash
# 1. 在 GitHub 网页上把 wavetermdev/waveterm fork 到自己账号/组织下
#    推荐同时重命名仓库为产品名(如 your-term / agentic-term)

# 2. clone 自己的 fork
git clone git@github.com:<你>/your-term.git
cd your-term

# 3. 添加 upstream 指向原仓库,用于后续同步
git remote add upstream https://github.com/wavetermdev/waveterm.git
git fetch upstream

# 4. 主分支跟踪自己的 fork,开发分支从 upstream/main 起步
git checkout -b feat/osc133-blocks upstream/main
```

**长期维护策略**:
- 每周或每月 `git fetch upstream && git merge upstream/main`,跟进 waveterm 的安全/性能修复。
- 改动尽量集中在独立目录(`electron/main/agent/`、`pkg/worktree/`、`frontend/app/agent-panel/` 等新增模块),减少合并冲突。
- 对 waveterm 已有文件的修改遵循"小而精",便于 rebase。

## M0 完成实况(2026-04-18) ✅

- Fork 已 detach(`s-zx/crest`,`fork: false`,进 contribution graph)
- 3 个 brand commits 已 push 到 `origin/main`
- 本地 `/Users/user/Documents/open-source/crest`,数据目录隔离到 `~/Library/Application Support/crest-dev/`,telemetry 彻底关闭

## waveterm 已有能力(不必重做)

- Shell 集成协议 OSC 16162(比 OSC 133 更丰富,带 JSON 元数据;bash/zsh/fish/pwsh 注入脚本完备)
- 后端 `ShellState`/`ShellLastCmd`/`ShellType`/`ShellIntegration` runtime info 已跟踪
- AI agent loop(Anthropic/OpenAI/Gemini/Google 多 provider)
- Tool use 框架 + tool approval 流程
- AI 面板 + 文件拖放 + AI diff 视图
- Monaco 编辑器集成

## M1 选定架构:xterm.js per block

放弃"单 xterm 连续滚屏",每条命令自己一个 xterm.js 实例 + block header/footer。复用 xterm 的 ANSI 解析/WebGL/搜索/序列化扩展。预估 6-8 周。

## M1 分阶段

| 状态 | 阶段 | 周 | 内容 |
|---|---|---|---|
| ✅ | **M1.1** | 2 | 后端:`pkg/cmdblock` 新包 + SQLite 表 `cmd_blocks` + schema migration;`pkg/shellexec` PTY 流在 OSC 16162 A/C/D 边界分段;wshrpc 事件 `cmdblock:started/chunk/done`。 |
| ✅ | **M1.2** | 2-3 | 前端:新 view type `term-blocks`(与旧 `term` 并存,设置切换);tanstack-virtual 虚拟化列表;每 block 渲染 header(cmd) + body(mini xterm.js) + footer(exit/duration/cwd);live block 自动滚动。 |
| 🔷 | **M1.3** | 1 | 输入区:CodeMirror 6 编辑器 fixed 底部;Enter 创建 pending block + 写入 PTY;上下键翻历史(从 block store 拉)。 |
| ✅ | **M1.4** | 1 | Alt-buffer 处理:`ESC[?1049h/l` 时当前 block 铺满整个视图,退出 alt buffer 回到 block 列表。 |
| 🔧 | **M1.5** | 1 | 键盘导航(`⌘↑/↓`/`⌘K` 过滤)+ 右键菜单(rerun / copy cmd / copy output)。 |
| 🔷 | **M1.6** | 1 | 长输出 per-block 虚拟化、二进制输出降级、Ctrl-C 中断 exit 130、scrollback 上限 + 归档到 SQLite。 |

## 关键改造文件(M1)

- **新建**:`pkg/cmdblock/{types.go,store.go,stream.go}`、`db/migrations/00XX_cmd_blocks.sql`、`frontend/app/view/termblocks/`
- **改造**:`pkg/shellexec/shellexec.go`(插入 OSC 16162 A/C/D 分段钩子)、`pkg/blockcontroller/shellcontroller.go`(转发分段事件到 `cmdblock` store)、`pkg/wshrpc/wshrpctypes.go`(加 `cmdblock:*` 事件 + `CmdBlockQuery` RPC)

## 首个开发迭代 = M1.1 的 step 1

1. 读 `pkg/shellexec/shellexec.go` + `pkg/blockcontroller/shellcontroller.go` + `pkg/util/shellutil/` 现有 OSC 16162 解析位置,理清 PTY 流转路径。
2. 设计 `CmdBlock` 类型 + SQLite schema(复用 wstore 的 migration 机制)。
3. 写 `pkg/cmdblock/store.go` 的 `InsertStart/AppendChunk/FinalizeDone` 三个方法。
4. 在 `pkg/shellexec` 的 PTY 读循环里加 hook:遇到 OSC 16162 A → `InsertStart`,C → 记录 cmd,D → `FinalizeDone`。
5. 单测:喂预录的 PTY 字节流(含 OSC 16162),断言 store 里有正确的 `cmd_blocks` 记录。

---

## M2 实际进度(截至 2026-04-19)

### 内置文件树 ✅（对应 Module 7）

完整实现 Warp 风格的文件浏览器，放置于 TopBar 下方的独立左侧 Panel。

**核心能力：**
- 懒加载树形视图，文件夹展开/收起，深层嵌套无限递归
- 文件图标主题：Simple Icons 官方品牌 Logo（TS/JS/Python/Go/Rust/Docker 等），Lucide 线条图标（文件夹/通用文件/图片/视频/锁等），tree-shake 后实际 bundle ~24 KB gzip
- **实时自动刷新**：通过 Electron 主进程 `fs.watch()` IPC 监听每个展开的目录，有文件变化时精准刷新对应目录，不刷全树
- **智能 cwd 跟随**：同 Tab 内点击不同 block 不改变根目录；在当前 terminal 执行 `cd` 命令后立即更新；切换到新 Tab 更新为新 Tab 的 terminal cwd
- **右键菜单**（对标 Warp）：New File、New Folder、cd to directory（注入到当前 focused terminal）、Open in new tab（新建 Wave tab + terminal）、Reveal in Finder、Rename（内联输入）、Delete、Copy Path、Copy Relative Path
- Header 操作按钮：New File、New Folder、Close

**相关文件：** `frontend/app/fileexplorer/`（新增），`pkg/waveobj/metaconsts.go`（新增 layout meta key）

### TopBar 顶部工具栏 ✅（新增）

替代原有 MacOSTabBarSpacer，永久展示在应用顶部（macOS 红绿灯区域右侧）。

| 区域 | 内容 |
|---|---|
| 左 | VTabBar 切换（⌘B）、File Explorer 切换、Workspace Switcher |
| 中 | 搜索占位符（⌘K，命令面板待实现） |
| 右 | Code Review、Notifications（block 完成通知）、GitHub 账号 |

**相关文件：** `frontend/app/topbar/topbar.tsx`（新增）

### Code Review 右侧边栏 ✅（对应 Module 9 部分）

本地 git diff 查看器，无需 GitHub 登录，对标 Warp 效果。

- 右侧 overlay 面板（380px/全屏可切换），`position:absolute` 渲染，不触发 terminal resize
- 文件列表：状态徽章（M/A/D）+ `+N • -N` stat + hover 操作图标（复制路径/Discard/Finder）
- 内联展开 diff：点击文件行展开着色 diff，懒加载
- **实时自动刷新**：`fs.watch()` 监听 `.git/` 目录和工作区根目录
- 底层使用新增 `RunLocalCmdCommand` RPC 执行 `git status` / `git diff`

**相关文件：** `frontend/app/codereview/`（新增），`pkg/wshrpc/wshserver/wshserver.go`（新增 RPC）

### Notifications 面板 ✅（新增）

订阅全局 `block:jobstatus` WPS 事件，terminal 命令完成时推送通知，点击可跳转到对应 block。**相关文件：** `frontend/app/notifications/`（新增）

### GitHub 账号面板 ✅（新增）

GitHub Token 登录，显示头像/名字，预留 PR/通知 API 扩展点。**相关文件：** `frontend/app/github/`（新增）

### 布局架构简化 ✅

- 移除 Wave AI Panel UI（保留后端 API 兼容 stub，避免影响已有 AI 功能）
- 移除嵌套 inner PanelGroup，改为平坦 3 列：`[VTabBar | FileExplorer | Content]`
- VTabBar 最小宽度 150px（react-resizable-panels minSize 原生限制）
- ⌘B 快捷键切换 VTabBar 显示/隐藏

### Shell 体验修复 ✅

**PROMPT_EOL_MARK=""**：在 `pkg/shellexec/shellexec.go` 所有 shell 启动路径（local/WSL/SSH/SSH job）中对 zsh 注入 `PROMPT_EOL_MARK=""`，消除每个 block 末尾的反白 `%` 字符。

**termblocks prompt 泄露修复**：done block 的 streaming cache 可能超出 `outputendoffset`（prompt 字节与最后输出 chunk 合并到达），`scheduleVisibleCheck` 现在检测并截断，消除 done block 末尾渗漏的 `# user @ ...` 和 `$`。

### 新增 Go RPC

`RunLocalCmdCommand(cmd, args, cwd) → {stdout, stderr, exitcode}`：服务端执行本地命令，Code Review 用此调用 `git status` / `git diff`，带 context 取消。

### Electron IPC 扩展

`watchDir(path, callback)` / `unwatchDir(path)`：主进程通过 `fs.watch()` 监听目录，`webContents.send` 推送事件，preload `contextBridge` 桥接给 renderer。文件树和 Code Review 的实时更新均依赖此 IPC。

---

## M1 实际进度(截至 2026-04-19)

按 commit 历史回填，与原计划做对齐：

### M1.1 后端 cmdblock + OSC 16162 + 事件 ✅

| commit | 说明 |
|---|---|
| `598bb8a1` | `cmdblock`: new package for per-command lifecycle tracking |
| `108d8663` | streaming OSC 16162 parser with offset tracking |
| `13d6c1c8` | wire tracker into the terminal PTY read loop |
| `7d3ad00c` | expose `GetCmdBlocks` via wshrpc, split types into cbtypes |
| `71fb8e15` | live streaming via wps events, replacing blind polling |

落地：`pkg/cmdblock/{tracker,types,cbtypes}.go` + 在 `pkg/blockcontroller/shellcontroller.go` 接入 + `pkg/wshrpc` 暴露事件与 RPC。

### M1.2 前端 termblocks view ✅（架构与计划略有偏差）

| commit | 说明 |
|---|---|
| `5a7066e7` | drop empty-Enter prompts + add termblocks view |
| `395b3898` | render per-block output via new `ReadBlockFileRange` RPC |
| `633cdc45` | size xterm to real visible lines |
| `49cd92b1` | auto-scroll to bottom + hide transient prompt rows |
| `f3344e5b` | focus running block for input, **make termblocks the default view** |

视觉与交互打磨：`462fb58f`（meta line + bold cmd）、`34863145`（status bar）、`c56358f7`（git branch + diff）、`a750da84`（FontAwesome chips + 语法高亮）、`59b20dde`（去掉旧 view header）、`df9cbf27`（紧凑空输出行）、`5f0e0bc3`（透明输出底色）。

**与计划的偏差**：没用 `tanstack-virtual` 做行级虚拟化（包仍在 deps，目前 termblocks 直接渲染列表）。短期问题不大，但 M1.6 真要处理大输出时需要回头加。

### M1.3 CodeMirror 输入区 🔷 未开始

当前输入是普通 textarea + ghost suggest（`1b2422a1`：clear command, real shell history, inline ghost suggest），**没换 CodeMirror 6**。多光标 / 语法高亮 / 多行 prompt 体验都还没做。

### M1.4 Alt-buffer 处理 ✅

| commit | 说明 |
|---|---|
| `4a3e589d` | `cmdblock`: alt-screen pass-through for interactive TUIs |
| `f10f747b` | send PTY resize when entering/fitting alt-screen |
| `37fa5da8` | fix hooks-order violation in alt-screen branch |

### M1.5 键盘导航 + 右键菜单 🔧 部分

- ✅ 右键菜单：`0ced9adc`（rerun / copy cmd / copy output）
- ✅ 计划外加成：`951d0e04` Ctrl-C / SIGINT support（这是 M1.6 列的项，提前完成）
- 🔷 `⌘↑/↓` 块间跳转、`⌘K` 命令面板：未实现

### M1.6 虚拟化 / scrollback 归档 🔷 未做

- 长输出 per-block 虚拟化：未做
- 二进制输出降级：未做
- scrollback 上限 + SQLite 归档：未做
- ✅ Ctrl-C 中断（已在 M1.5 项中提前完成）

### 计划外完成（属于 Module 3 / 整体打磨）

**Module 3「垂直 Tab + Git 元数据」整块完成**（原计划放在 M2-M3 之外的横向模块，这一轮顺手做了）：

| commit | 说明 |
|---|---|
| `46072fbb` | default the tab bar to the left (vertical) like Warp |
| `f550f4c9` | show cwd, git branch, diff counts under each tab name |
| `12b19acd` | cwd replaces default `T<n>` name + glassier active highlight |
| `0a2f91fd` / `b05a333c` | active highlight 调整至 Warp 风 |
| `047231d4` / `8d7db762` / `3cacdceb` / `57e6fb25` | 行高、metadata row、hover ⋮/× 按钮、flagColor 边条 |

**已知未解决问题**：vtabs 新建标签时**首屏闪烁**。多次 revert / redo（`58082f81` / `259fc81b` / `01d69dfb` / `d388116f` / `11cf0e68` 等），最近一轮把"WebContentsView 预涂背景 + index.html 内联深色 bg"的方案重新接回（`fc852909`、`11cf0e68`），尚需观察。

---

## 下一步

M2 阶段主要交付已完成（文件树、Code Review、TopBar、布局简化、Shell 修复）。当前空缺和建议优先级如下：

### 已完成（M2 阶段）✅
- 内置文件树（懒加载 + fs.watch 实时刷新 + 右键操作 + cwd 跟随）
- TopBar（VTabBar/FileExplorer 切换 + Workspace Switcher + Code Review/Notifications/GitHub 入口）
- Code Review 面板（本地 git diff，无需 GitHub 登录）
- Block notifications（termblocks block:jobstatus 订阅）
- 布局简化（移除 AI Panel UI，平坦 3 列 Panel）
- Shell 修复（PROMPT_EOL_MARK + prompt 字节泄露截断）

### 下阶段建议优先级

1. **⌘K 全局命令面板**（TopBar 搜索框当前是占位符，连接后可搜文件/命令/block 历史）
2. **M1.3 — CodeMirror 6 输入区**（当前 textarea 是 agent 输入的体验瓶颈）
3. **M1.5 余项 — `⌘↑/↓` 块间跳转**（小改动，提升 termblocks 导航体验）
4. **M1.6 — per-block 虚拟化 + scrollback 归档**（`tail -f` / 大输出场景必须）
5. **Code Review 逐行评论 → Send to Agent**（Module 9 核心差异化功能）
6. 进入 **M3 — Agent SDK 接入 + 追问 + `@`-文件引用**

> 内置编辑器 + LSP、多 Agent worktree、Diff 预览确认 均为 🔷 未开始。
