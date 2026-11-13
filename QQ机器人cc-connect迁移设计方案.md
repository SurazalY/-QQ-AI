# QQ AI 机器人迁移至 cc-connect 设计方案

> 状态：新链路正在运行，固定使用 Antigravity 原生 Gemini 3.8 Flash。主人私聊、多轮与识图已通过；新增群 `823658226` 的普通聊天及 @ 两轮均已实际送达，主人确认可用。
> 制定时间：2026-11-09  
> 核心原则：**极简改造、最大化复用 `cc-connect` 与 `agy` 原生能力、保留「乖巧小鲸鱼」人设与群白名单、不动现有 SnowLuma 容器与 DSH 冻结副本**

---

## 实施结果（2026-11-09—10）

- **源码与构建**：上游 `chenhg5/cc-connect` 基线 `619f884`；Go 1.27.3 位于项目内 `.tools/go/`，没有安装 Homebrew。已编译仅包含 ACP / QQ 的 `cc-connect`。
- **配置与资产**：本目录 `config.toml` 权限为 `0600`，分别读取现有 SnowLuma 的 `networks.wsServers[0]` 与 `networks.httpServers[0]` Token，未改 SnowLuma 配置。原人设、QQ 记忆和 19 张收藏表情已迁移至 `whale-workspace/`，保留原备注及使用说明。含凭据配置、工具链、工作区与 `.cc-data/` 均不纳入 Git。
- **服务范围**：仅主人私聊 `839107207` 与群 `706636822`、`823658226`。当前联调使用 `823658226`。群内共享会话；群友禁用内置命令，工具授权仅由主人批准。新链路不依赖 DSH，没有重新启用旧桥接。
- **QQ 适配器**：完成双 Token、私聊/群白名单、`[SILENT]` 过滤、最多三条且间隔 800ms 的按行发送。接收循环异步处理消息，持续读取 OneBot API 回执，消除了同步处理造成的阻塞。保留 @ 目标，并将 @ 机器人明确传为 `[QQ：此消息直接 @ 了你]`。
- **忙时合并与显示**：按主人确认的方案，完整旁听群消息；忙时复用现有队列合并为一个下一轮批次，保留每条发言人的身份、@ 信息和附件。私聊维持逐条行为。不增加定时器或独立后台队列。现有 `quiet` 模式隐藏正常排队和队列满提示；系统错误通过独立错误发送入口转至主人 `839107207` 私聊，附来源群号；原生 agy 文本警告也转私聊，保留多行诊断。QQ 群消息、图片及文件发送失败同样通知主人，通知失败保留本地日志，不回发群聊。
- **ACP 与模型**：使用 `/Users/yak/.local/bin/agy acp --sandbox --log-file /dev/null`，模式 `accept-edits`。原默认 `gemini-4-argon-medium` 实际返回 503 容量不足，导致长时间原生重试；当前通过原生 ACP 模型选择明确配置 `gemini-3.8-flash`，没有自动切换模型逻辑。原群 ACP 会话在失败后持续报执行终止，已新建原生会话并接续保存的有效群聊上下文；旧会话数据、cc-connect 历史和长期记忆均保留，新会话恢复后再次加载也已验证成功。
- **表情包授权**：主人已选择自动授权固定表情命令。`auto_approve_commands` 只包含现有 19 张表情的精确 `cc-connect send --image stickers/<文件名>` 命令，通过原生 ACP 权限响应处理普通执行及 `BypassSandbox` 请求；其他工具请求仍交给主人。真实工具发图已通过。
- **接话规则**：每轮一到三句短话、每句一行，发送端最多三条；刚回复且没有新话题或直接提问时潜水，避免逐条跟话。无需 @，普通聊天中有能接的话题时自然接梗、附和或追问；明确不宜插话、重复刷屏或刚说完没有新内容时才潜水。仍仅由新消息触发，heartbeat / cron 保持关闭。

**已验证**：相关 QQ、ACP、合并队列及授权测试通过；真实 ACP 握手、多轮、读写文件、内置搜索、图片落盘识别通过；Flash 文本与图片请求通过；原群重建后原生会话的上下文恢复和再次加载通过。QQ 主人私聊两轮及识图已由主人确认；群 @ 曾得到三条分行回复并由主人确认；连续 25 条群消息期间未出现排队或队列满提示。恢复后的原群 Flash 会话能对无 @ 的普通聊天生成正常短回复。2026-11-10 00:18—00:19，新群 `823658226` 真实普通聊天及随后 @ 消息均完成 ACP 两轮处理，并由 SnowLuma 历史确认分别送达两条、三条回复；该次两轮约 29 秒、39 秒，无队列或 API 状态提示。主人确认新群可用。

**小调整验证（2026-11-10）**：真实 QQ 接口已验证：不带错误图标的语义错误通知与原生警告均只送到主人私聊，新群历史中均没有出现；四行诊断完整保留为一条私聊通知。四行普通回复实发前三条，第四条未发送。针对性测试与 ACP/QQ 构建通过。

**限制/未验证**：原群 `706636822` 最后核对的禁言截止为 2026-11-10 01:05:29，禁言期间发送返回 `retcode=100`；没有擅自解除群禁言。后续测试已按主人要求移至 `823658226` 并通过。冻结备份没有做实际恢复演练，不能承诺“三秒回切”。

启动新链路：

```sh
cd /Users/yak/Project/大肥鱼/cc-connect
./cc-connect --config /Users/yak/Project/大肥鱼/cc-connect/config.toml
```

前台运行时用 `Ctrl+C` 停止。旧 SurfCore 与冻结备份保持原状，恢复操作以冻结备份中的《恢复说明.txt》为准。

下文保留原设计；实际改动与验收状态以上述实施结果为准。

---

## 一、 方案背景与总体定位

### 1.1 迁移目标
将当前运行于 macOS (Apple Silicon) 上的 QQ 机器人链路：
```text
QQ ↔ SnowLuma (Docker) ↔ OneBot v11 ↔ SurfCore (bridge.js + DSH 插件) ↔ DSH NEXT ↔ 模型服务
```
迁移并简化为：
```text
QQ ↔ SnowLuma (Docker) ↔ OneBot v11 (:3000/:3001) ↔ cc-connect (微调版) ↔ agy acp (本机 1.5.3)
```

### 1.2 已确认的核心取舍
1. **底层 Agent 接入**：采用 `cc-connect` 的 `type = "acp"` 适配器，启动本机官方 `agy acp` 常驻子进程，复用本机 Antigravity 订阅额度，不依赖 Gemini API Key，不再维护 DSH NEXT 桌面端逆向桥接。
2. **改造范围最小化**：不搬运 SurfCore 复杂的 `reserved2` 阻塞式等待（如 `qq_wait_for_messages` 挂起 5 分钟）或水位状态机；**只对 `cc-connect` 的 `platform/qq/qq.go` 做一处约 40 行的轻量级补丁**（补齐双 Token、群白名单、`[SILENT]` 自主潜水过滤、换行分条发送），其余全部复用 `cc-connect` 原生引擎。
3. **单 Project 统一承载**：由于目标群很小且私聊同样以「乖巧小鲸鱼」身份交互，仅配置 **1 个 Project** 同时服务主人私聊（`839107207`）与目标群（`706636822`），通过 `cc-connect` 原生的 `[projects.users.roles]` 实现主人与群友的权限隔离。
4. **冷群静默策略**：保持 `cc-connect` 默认关闭 `heartbeat` 与 `cron` 的状态，群内无人发言时 100% 被动静默，不主动后台唤醒找话题，兼顾体验与订阅账号风控安全。
5. **人设、记忆与表情包轻量化**：复用 `agy` 原生工作区机制（`GEMINI.md` 注入人设、`memory.md` 存储长期记忆、`stickers/` 配合 `cc-connect send --image` 发送表情包），不迁移 SurfCore 庞大的本地数据库与插件体系。

---

## 二、 现有基建复用与环境核查清单

| 组件 / 资产 | 现有路径或端点 | 迁移处理方式 | 状态说明 |
| --- | --- | --- | --- |
| **SnowLuma 容器** | Docker 容器 `snowluma`（QQ `3905380281`） | **原样复用** | 保持运行，不重登、不改容器配置 |
| **OneBot WS 端点** | `ws://127.0.0.1:3001` | **原样复用** | 供 `cc-connect` 收发消息事件（使用 WS 专属 Token） |
| **OneBot HTTP 端点** | `http://127.0.0.1:3000` | **原样复用** | 供 `cc-connect` 下载/上传群文件（使用 HTTP 专属 Token） |
| **Token 配置文件** | `~/.dsh/surfcore/snowluma-home/config/onebot_3905380281.json` | **只读提取** | 从中读取 `wsServers[0].accessToken` 与 `httpServers[0].accessToken` 填入 `cc-connect` 配置 |
| **本机 `agy` CLI** | `agy` (`v1.5.3`) | **原样复用** | 通过 `agy acp` 启动标准 JSON-RPC 会话 |
| **人设文件** | `_backups/.../char-mvyty824/persona.md` | **提取文本** | 转为小鲸鱼工作区下的 `GEMINI.md` |
| **QQ 记忆卡** | `_backups/.../char-mvyty824/memory/memory-card-prompt-qq.md` | **提取文本** | 转为小鲸鱼工作区下的 `memory.md` 初版 |
| **收藏表情包** | `_backups/.../surfcore/state/stickers.json` | **按需导出** | 整理为 `stickers/` 图片目录与 `stickers.md` 索引 |
| **安全搜索 MCP** | `_backups/.../surfcore/src/mcp-web-search-safe.js` | **可选挂载** | `agy` 已内置 `search_web`；该脚本可配合搜索 API Key 额外提供安全的 `web_fetch` |
| **Go 编译环境** | 本机当前未检测到 `go` 命令 | **实施前安装** | 实施阶段需先执行 `brew install go` 以编译打补丁后的 `cc-connect` |
| **DSH 冻结副本** | `/Users/yak/Project/大肥鱼/_backups/dsh-frozen-20261109-204116` | **严格只读保护** | 作为随时回切的基线，严禁修改或覆盖 |

---

## 三、 `cc-connect` 最小化源码改造设计

整个项目**只修改一个源码文件**：`platform/qq/qq.go`（不改动 `core/` 引擎与 `agent/acp` 核心代码）。

### 3.1 改造点 1：SnowLuma HTTP / WS 双 Token 解耦
- **现状问题**：上游 `platform/qq/qq.go` 只有一个 `token` 字段，WS 连接与 `callHTTPAPI` 共用同一个 Token，导致 SnowLuma 下必有一端报 `401 (retcode 1401)`。
- **修改方案**：
  - 在 `Platform` 结构体中新增 `httpToken string` 字段。
  - 在 `New(opts)` 中读取 `http_token`（若未配置则默认回退使用 `token`，保持上游兼容）。
  - 在 `callHTTPAPI()` 中优先使用 `p.httpToken` 设置 `Authorization: Bearer <httpToken>`。

### 3.2 改造点 2：群聊白名单 (`allow_groups`)
- **现状问题**：上游 `platform/qq/qq.go` 仅有按用户过滤的 `allow_from`，没有按群号过滤的白名单。
- **修改方案**：
  - 在 `Platform` 结构体中新增 `allowGroups string`（逗号分隔的群号列表，如 `"706636822"`，空或 `"*"` 表示不限制）。
  - 新增辅助方法 `isGroupAllowed(groupID int64) bool`。
  - 在 `handleMessage()` 中，当 `msgType == "group"` 时，首先校验 `p.isGroupAllowed(groupID)`；未命中的群直接静默丢弃（`return`），绝不进入后续下载图片或唤醒 Agent 的流程。

### 3.3 改造点 3：全量看群下的 `[SILENT]` 自主潜水过滤
- **现状问题**：当 `require_mention = false` 时，群内每条消息都会送入 Agent，而上游 `Send()` 会无条件把 Agent 输出发回群里，导致机器人变“话痨客服”。
- **修改方案**：
  - 在 `Send(ctx, replyCtx, content)` 开头增加静默判断：
    - 若 `strings.TrimSpace(content) == ""` 或 `strings.TrimSpace(content) == "[SILENT]"`（以及包含 `[SILENT]` 标记且无实质回复），直接返回 `nil`，不调用 OneBot 发送接口。
  - 配合 `GEMINI.md` 中的人设规则，让小鲸鱼在群友互相闲聊、插不上话或无需接话时只输出 `[SILENT]`，用 5 行代码还原以前 `reserved2` 的“旁听群聊、随缘接梗、随时潜水”体验。

### 3.4 改造点 4：多行短句自然分条发送 (`split_lines`)
- **现状问题**：小鲸鱼人设要求“一条一行，不换行分段；一句话一条，补充时自然隔开”，但 `cc-connect` 在单轮对话结束时会将整轮文本一次性传入 `Send()`，默认在 QQ 里会变成带换行的一整块消息气泡。
- **修改方案**：
  - 在 `Platform` 新增布尔配置 `splitLines`（`split_lines = true`）。
  - 当 `splitLines` 开启时，`Send()` 将 `content` 按换行符 `\n` 拆分为多条非空短句（过滤掉空行；若超过最大条数如 `4` 条则将剩余部分合并为最后一条，防止刷屏）。
  - 依次调用 `send_group_msg` / `send_private_msg` 发送每一条，并在相邻两条之间加入 `600ms ~ 1000ms` 的短暂停顿，还原真人打字分条发送的节奏感。

---

## 四、 `config.toml` 核心配置设计（脱敏版）

以下配置完全基于 `cc-connect` 原生能力，将“编程终端工具”伪装收敛为纯粹的「乖巧小鲸鱼」群友：

```toml
language = "zh"
shell = "/bin/zsh"
data_dir = "/Users/yak/Project/大肥鱼/cc-connect/.cc-data"
attachment_send = "on"
busy_timeout_mins = 2

[log]
level = "info"

# 全局显示控制：彻底隐藏思考过程、工具调用进度和底部代码助手状态栏
[display]
mode = "compact"
thinking_messages = false
tool_messages = false
reply_footer = false
show_context_indicator = false
show_workdir_indicator = false
hide_agent_footer = true

# 出站速率保护：防止突发连发触发 QQ 风控
[outgoing_rate_limit]
max_per_second = 1.5
burst = 3

[[projects]]
name = "whale-qq"
admin_from = "839107207"          # 仅主人可执行 /restart、/shell 等管理命令
inject_sender = true              # 自动向 Agent 注入发言人昵称与 QQ 号
reset_on_idle_mins = 0            # 禁用 30 分钟空闲自动开新会话，保持群聊记忆连续
reply_footer = false
show_context_indicator = false
show_workdir_indicator = false

# 角色与权限隔离（复用 core/user_roles.go）
[projects.users]
default_role = "member"

[projects.users.roles.admin]
user_ids = ["839107207"]
disabled_commands = []
rate_limit = { max_messages = 60, window_secs = 60 }

[projects.users.roles.member]
user_ids = ["*"]
disabled_commands = ["*"]         # 群友禁用所有 / 斜杠内置命令，纯聊天交互
rate_limit = { max_messages = 10, window_secs = 60 }

# Agent 配置：通过 ACP 协议驱动本机 agy 1.5.3
[projects.agent]
type = "acp"

[projects.agent.options]
work_dir = "/Users/yak/Project/大肥鱼/cc-connect/whale-workspace"
cmd = "agy"
args = [
  "acp",
  "--sandbox"                     # 开启终端沙箱限制，防止群聊注入越权操作宿主系统
]
display_name = "Whale ACP"

# 平台配置：对接现有 Docker SnowLuma OneBot v11
[[projects.platforms]]
type = "qq"

[projects.platforms.options]
ws_url = "ws://127.0.0.1:3001"
token = "${SNOWLUMA_WS_TOKEN}"           # 填入 onebot_3905380281.json 的 ws accessToken
http_url = "http://127.0.0.1:3000"
http_token = "${SNOWLUMA_HTTP_TOKEN}"    # 填入 onebot_3905380281.json 的 http accessToken
allow_from = "*"                         # 允许群内成员发言（受限于下方 allow_groups）
allow_groups = "706636822"               # ★ 仅响应「枪王之王」群，其他群全部静默忽略
require_mention = false                  # 全量看群，由模型自主决定回复或输出 [SILENT]
share_session_in_channel = true          # 群内全员共享同一个会话 qq:g:706636822
split_lines = true                       # 按行拆分为多条短消息自然发送
```

> **私聊安全补充说明**：若你不希望除你之外的任何陌生人通过私聊触发机器人，我们可在 `platform/qq/qq.go` 的 `allow_groups` 逻辑旁加一行：`msgType == "private"` 时仅放行 `allow_private = "839107207"`（主人私聊），这样私聊只服务你一人，群聊服务 `706636822` 全员，和旧 SurfCore 的 `allow.private` + `allow.groups` 100% 对齐。

---

## 五、 专属工作区 (`whale-workspace`) 与能力落地设计

在 `/Users/yak/Project/大肥鱼/cc-connect/whale-workspace/` 下构建小鲸鱼的独立沙箱目录，目录结构如下：

```text
/Users/yak/Project/大肥鱼/cc-connect/whale-workspace/
├── GEMINI.md             # 合并人设 (persona.md) + 群聊接话/[SILENT]规则 + 工具使用说明
├── memory.md             # 从 memory-card-prompt-qq.md 导出的初始长期记忆（支持后续读写更新）
├── stickers.md           # 收藏表情包的编号、情绪、画面描述索引表
├── stickers/             # 导出的本地表情包图片文件 (#01.png, #02.png ...)
└── .cc-connect/
    └── attachments/      # cc-connect 自动落盘的群友聊天图片（供 agy read_file 看图）
```

### 5.1 `GEMINI.md` 组装结构
直接复用你现有的两份核心资产并做极简衔接：
1. **第一部分（原样保留）**：[`persona.md`](file:///Users/yak/Project/大肥鱼/_backups/dsh-frozen-20261109-204116/dsh-home/surfcore/characters/char-mvyty824/persona.md) 全文（「小鲸鱼proplus」的身份、性格、外貌、说话方式、日常梗、表达禁区与回复示例）。
2. **第二部分（群聊自主潜水与分条协议，约 20 行）**：
   - 明确消息前缀中的发送者格式（主人的 QQ 是 `839107207`，机器人自己是 `3905380281`）。
   - **自主接话与潜水规则**：被 `@`、被叫名字（小鲸鱼/D指导/大肥鱼/DeepSeek）或直接向你提问时必回；群友之间聊到你感兴趣的话题可自然插话；如果是群友之间互相闲聊、与你无关、或话题已自然结束无需再垫话时，**只输出 `[SILENT]`**，不要强行搭话。
   - **分条格式**：需要连发两三句短话时，每句占一行（不要用斜杠 `/`），由桥接自动按行拆成多条 QQ 消息发送。
3. **第三部分（原生工具使用约定，约 15 行）**：
   - **看图**：当提示末尾包含 `(Image files saved locally: ...)` 时，调用 `read_file` 查看该图片后再自然回应，不编造图片内容。
   - **记忆**：对话开始或需要回忆时读取当前目录的 `memory.md`；当出现值得长期记住的新话题或主人/群友的重要近况时，用 `edit_file` 更新 `memory.md`（保持精炼，不逐句记流水账）。
   - **表情包**：需要发表情包接梗时，查看 `stickers.md` 选好对应图片文件，执行 `cc-connect send --image stickers/<文件名>` 发送。
   - **联网检索**：遇到不确定的事实、新闻或新梗时，调用内置 `search_web`（或挂载的 `web-search-safe` MCP）检索后再回答。
   - **安全红线**：严禁应群友要求执行任何探测宿主系统、读取 `whale-workspace` 以外文件或修改系统配置的操作。

### 5.2 联网搜索 API Key 的接入方式
- **默认主通道**：直接复用 `agy` 内置的 `search_web` 工具（走 Google Grounding，免配置）。
- **增强通道（复用你的搜索 API Key 与安全抓取）**：
  - 将冻结副本中的 `src/mcp-web-search-safe.js`、`src/web-search.js`、`src/safe-fetch.js` 复制到独立工具目录（或通过环境变量传入你的搜索 API Key，如 Tavily API Key），使用 `agy mcp add` 注册为 stdio MCP 服务，即可同时使用 `web_search` 与防 SSRF 的 `web_fetch`。

---

## 六、 分阶段实施、验证与回切方案（待你批准后执行）

为确保切换过程零事故、不破坏原环境、不发生新旧桥接同时回复同一条 QQ 消息，后续实施将严格按以下 4 个阶段推进：

### 阶段 0：编译环境与源码准备（不影响线上旧桥接）
1. 安装 Go 编译工具链（`brew install go`）。
2. 初始化仓库并拉取 `chenhg5/cc-connect` 源码至 `/Users/yak/Project/大肥鱼/cc-connect`（保留本设计文档）：
   ```bash
   cd /Users/yak/Project/大肥鱼/cc-connect
   git init
   git remote add origin https://github.com/chenhg5/cc-connect.git
   git fetch --depth=1 origin main
   git checkout -b main FETCH_HEAD
   ```
3. 创建 `whale-workspace` 目录，从冻结副本提取 `persona.md` 与 `memory-card-prompt-qq.md` 生成 `GEMINI.md` 和 `memory.md`。

### 阶段 1：脱离 QQ 的 `agy acp` 专项兼容性验证（此时不连 SnowLuma）
在修改或连接 QQ 之前，先针对我们在分析阶段识别出的 **3 个未验证风险点**做离线实测：
1. **验证 `agy 1.5.3 acp` 握手与多轮对话**：确认 `initialize` -> `session/new` -> `session/prompt` 在本机正常流式返回。
2. **验证 `"mcpServers": []` 传参影响**：确认 `cc-connect` 在 `session/new` 中传空数组时，`agy acp` 是否仍能正常使用内置工具（`read_file`、`search_web`）及全局 MCP；若会被清空，则在代码中顺手去掉该空数组覆盖。
3. **验证看图与沙箱边界**：确认 `--sandbox` 模式下 `agy acp` 能否正常通过 `read_file` 读取 `whale-workspace/.cc-connect/attachments/` 下的测试图片。

### 阶段 2：打入 `platform/qq/qq.go` 微调补丁并编译
1. 在 `platform/qq/qq.go` 中打入上述 4 项轻量改造（`http_token`、`allow_groups` + `allow_private`、`[SILENT]` 过滤、`split_lines` 按行拆条发送）。
2. 执行 `go test ./platform/qq/...` 与 `go build -o cc-connect ./cmd/cc-connect`，确保编译与单元测试全部通过。

### 阶段 3：互斥切换测试与 3 秒回切保障（防双响 SOP）
由于 SnowLuma 的 `:3001` 允许多个 WS 客户端同时连接，**严禁新旧桥接同时在线监听同一个群/私聊**：
1. **暂停旧桥接（保留 DSH NEXT 进程不关）**：
   按 [`SurfCore-QQ桥接运维手册.md` 第 8.1 节](file:///Users/yak/Project/大肥鱼/SurfCore-QQ桥接运维手册.md#L287-L296)，读取 `/Users/yak/Documents/deepseek-harness/default-workspace/surfcore/state/bridge.lock` 的 PID 发送 `SIGTERM`，并确认 `127.0.0.1:3100` 停止监听。
2. **启动新 `cc-connect` 进行实测**：
   在前台带日志启动编译好的 `./cc-connect -c config.toml`，依次验证：
   - 主人私聊（`839107207`）文字对话、分条效果、看图效果；
   - 目标群（`706636822`）群友发言时的接话效果，以及无关发言时的 `[SILENT]` 潜水效果。
3. **随时 3 秒回切旧环境（若效果不满意或需继续调优）**：
   - `Ctrl+C` 停止 `cc-connect`；
   - 在旧 SurfCore 目录执行 `./start-bridge-macos.sh` 重新拉起旧桥接，立即恢复原状。

---

## 七、 服务条款（ToS）与长期运行注意事项

1. **订阅合规边界**：本方案虽然通过官方 `agy acp` 协议调用本机 CLI 且不提取 OAuth Token，但将个人订阅用于群聊机器人仍属于非典型的自动化场景。
2. **已采取的保护措施**：
   - 仅限单个小群（`706636822`）与本人私聊（`839107207`）白名单准入；
   - 关闭所有后台定时唤醒（`heartbeat` / `cron`），冷群零请求；
   - 开启角色级速率限制（群友每分钟最多 10 条），防止异常刷屏触发上游限流或风控。
