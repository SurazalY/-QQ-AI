# 服务器搬迁与部署指南（面向服务器 AI / 运维）

本仓库是基于 `cc-connect` 二次开发的 **QQ AI 机器人（小鲸鱼 / Whale）** 项目，已将核心源码、Web 管理/监控面板改造、脱敏配置模板以及机器人工作区资产打包归档。

如果你是目标 Linux 服务器上的 AI 助手，请按本文档逐步完成环境准备、编译构建、配置迁移、服务托管与上线验收。

---

## 一、仓库已包含 vs 需由主人单独补齐的内容

### 仓库内已包含（`git clone` 后即具备）
- **后端源码与定制改动**：Go 源码（`cmd/`、`core/`、`agent/`、`platform/`、`config/`、`daemon/` 等），包含：
  - `platform/qq`：SnowLuma / OneBot v11 双 Token（WS `token` + HTTP `http_token`）、群/私聊白名单、`[SILENT]` 过滤、按行分条发送、异步接收循环、@ 识别与忙时消息合并等改造。
  - `agent/acp` / `agent/antigravity`：ACP 模型选择、进程探针与跨平台进程组管理。
  - `core/`：Monitor 监控接口、ErrorSender 私聊告警、代理环境变量处理、管理 API 扩展等。
- **前端源码**：`web/`（含新增的 Monitor 监控面板组件 `web/src/components/Monitor/` 与多语言文案）。
- **机器人工作区资产**：
  - `whale-workspace/`：主群与主人私聊工作区，包含 `GEMINI.md`（人设与运行约定）、`memory.md`（长期记忆）、`stickers.md`（表情包索引）和 `stickers/`（19 张表情包）。
  - `whale-workspace-8826812/`：独立私聊隔离工作区，包含对应的 `GEMINI.md`、`memory.md`、`stickers.md`、`stickers/`。
- **配置模板与设计文档**：
  - `config.whale.example.toml`：从原生产环境导出的**脱敏配置模板**。
  - `QQ机器人cc-connect迁移设计方案.md`：架构改造与历史设计背景。

### 仓库故意未包含（安全与体量原因，需在服务器补齐）
1. **真实 `config.toml`**：包含 OneBot HTTP/WS Token、Bridge Token、Management Token（已被 `.gitignore` 忽略）。请向主人索取原 `config.toml` 或基于 `config.whale.example.toml` 填入真实 Token。
2. **`.cc-data/` 运行态目录**：旧机日志与历史会话快照未入库。首次启动会自动重建；若主人需要延续旧会话历史，可请主人私密传输原 `.cc-data/sessions/`。
3. **`whale-workspace/.cc-connect/attachments/`**：旧机约 170 MB 的历史群聊图片缓存，未入库，启动后会自动重新创建目录。
4. **外部运行依赖**：Linux 版 `agy`（Antigravity CLI）及其登录态、QQ OneBot v11 协议端（如 SnowLuma / NapCat）及其 QQ 登录态。

---

## 二、目标服务器环境依赖

部署前请先检查并安装以下依赖（假定目标部署目录为 `/opt/cc-connect`，如使用其他目录请同步替换下文路径）：

1. **Go**：版本 `>= 1.25.0`（见 `go.mod`；原开发环境为 Go 1.27）。
2. **Node.js + npm**（或 pnpm）：建议 Node.js `>= 20`，用于构建嵌入二进制的 Web 管理与监控前端 `web/dist/`。
3. **Antigravity CLI (`agy`)**：
   - 安装与服务器 CPU 架构（`linux/amd64` 或 `linux/arm64`）匹配的 `agy`。
   - **必须完成交互登录认证**，并验证 `agy acp` 能正常启动且有权限调用配置中的模型（当前配置为 `gemini-4-hydrogen`）。
4. **QQ OneBot v11 协议端**（如 SnowLuma / NapCat）：
   - 需提供 OneBot HTTP API（默认 `http://127.0.0.1:3000`）与正向 WebSocket（默认 `ws://127.0.0.1:3001`）。
   - 注意 SnowLuma 的 HTTP 与 WS 使用**两个不同的 Access Token**，不要填混。

---

## 三、分步部署流程

### Step 1：编译前端与 Go 二进制

> `web/embed.go` 通过 `//go:embed all:dist` 将前端静态资源直接编译进 `cc-connect` 单二进制文件，因此必须先构建 `web/dist` 再执行 `go build`。

```bash
cd /opt/cc-connect

# 1. 构建前端（生成 web/dist/）
cd web
npm install
npm run build
cd ..

# 2. 构建后端二进制
go build -o cc-connect ./cmd/cc-connect

# （可选）如果只想编译本次用到的 ACP/Antigravity + QQ 以减小体积与编译耗时：
# make build AGENTS=acp,antigravity PLATFORMS_INCLUDE=qq
```

### Step 2：将 `cc-connect` 加入系统 `PATH`（关键！）

**务必把编译好的 `cc-connect` 安装或软链接到 `/usr/local/bin/cc-connect`：**

```bash
sudo ln -sf /opt/cc-connect/cc-connect /usr/local/bin/cc-connect
which cc-connect
```

> **原因**：`whale-workspace/GEMINI.md` 中约定机器人发送表情包时会执行命令 `cc-connect send --image stickers/<文件名>`。如果 `cc-connect` 不在子进程 `PATH` 中，发表情包功能会失败。

### Step 3：生成并修改生产配置 `config.toml`

```bash
cd /opt/cc-connect
cp config.whale.example.toml config.toml
chmod 600 config.toml
```

逐项检查并修改 `config.toml` 中以下关键字段：

| 配置项 | 模板默认值 | 需要修改为 |
| :--- | :--- | :--- |
| `data_dir` | `"/opt/cc-connect/.cc-data"` | 服务器实际部署路径下的 `.cc-data` |
| `shell` | `"/bin/bash"` | 确认服务器存在 `/bin/bash`（原 macOS 为 `/bin/zsh`） |
| `projects[*].agent.options.cmd` | `"/usr/local/bin/agy"` | 服务器上 `which agy` 返回的真实绝对路径 |
| `projects[0].agent.options.work_dir` | `"/opt/cc-connect/whale-workspace"` | 服务器实际 `whale-workspace` 绝对路径 |
| `projects[1].agent.options.work_dir` | `"/opt/cc-connect/whale-workspace-8826812"` | 服务器实际 `whale-workspace-8826812` 绝对路径 |
| `projects[*].agent.options.env` | `HTTP_PROXY` / `HTTPS_PROXY = "http://127.0.0.1:7897"` | **按服务器网络环境调整**：若服务器直连外网无需代理，直接删除代理环境变量；若使用其他代理端口，改为实际地址 |
| `projects[*].platforms[0].options.http_url` / `ws_url` | `127.0.0.1:3000` / `3001` | 改为实际 QQ OneBot 协议端地址 |
| `projects[*].platforms[0].options.token` | `<REPLACE_WITH_ONEBOT_WS_TOKEN>` | 填入 OneBot **WS** Access Token |
| `projects[*].platforms[0].options.http_token` | `<REPLACE_WITH_ONEBOT_HTTP_TOKEN>` | 填入 OneBot **HTTP** Access Token |
| `bridge.token` / `management.token` | `<REPLACE_WITH_...>` | 填入高强度随机 Token（如 `openssl rand -hex 24`） |

### Step 4：验证外部依赖（`agy` 与 OneBot 协议端）

```bash
# 1. 验证 agy 可执行且已登录
/usr/local/bin/agy --version

# 2. 验证 OneBot HTTP 端口可达（将 <HTTP_TOKEN> 替换为真实值）
curl -sS -H "Authorization: Bearer <HTTP_TOKEN>" http://127.0.0.1:3000/get_login_info
```

### Step 5：前台试跑验证

```bash
cd /opt/cc-connect
./cc-connect --config /opt/cc-connect/config.toml
```

观察启动日志，确认：
- 两个项目 `whale-qq` 和 `whale-qq-8826812` 均成功连接 OneBot WebSocket；
- Bridge (`:9810`) 和 Management Web UI (`:9820`) 正常监听；
- 无 `401` / `retcode 1401`（双 Token 填反时的典型错误）或路径不存在报错。

### Step 6：配置 `systemd` 常驻守护

试跑正常后，按 Ctrl+C 停止前台进程，创建 systemd 服务（也可以评估使用项目自带的 `./cc-connect daemon install`，或直接创建如下 unit 文件以便精确控制用户与 `PATH`）：

```ini
# /etc/systemd/system/cc-connect-whale.service
[Unit]
Description=CC-Connect Whale QQ AI Bot
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
User=<RUN_USER>
WorkingDirectory=/opt/cc-connect
Environment="PATH=/usr/local/bin:/usr/bin:/bin:/home/<RUN_USER>/.local/bin"
ExecStart=/opt/cc-connect/cc-connect --config /opt/cc-connect/config.toml
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

启用并启动服务：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now cc-connect-whale.service
sudo systemctl status cc-connect-whale.service
journalctl -u cc-connect-whale.service -f
```

> **重要**：`User=<RUN_USER>` 必须与完成 `agy` 登录认证的系统用户一致，否则 `agy` 读取不到登录凭据。

---

## 四、安全检查清单

1. **`config.toml` 权限**：确认文件权限为 `600`，且从未被 `git add`。
2. **端口暴露检查**：
   - `3000` / `3001`（OneBot）：只应监听在 `127.0.0.1` 或内网，禁止公网裸奔。
   - `9810`（Bridge）与 `9820`（Management Web UI）：建议通过防火墙限制仅内网/SSH 隧道访问，或通过 Nginx/Caddy 反代并配置强 Token 与限制 `cors_origins`。
3. **运行目录备份**：建议定期备份服务器上的 `config.toml`、`whale-workspace/memory.md`、`whale-workspace-8826812/memory.md` 与 `.cc-data/sessions/`。

---

## 五、上线验收清单

- [ ] `systemctl status cc-connect-whale` 为 `active (running)`
- [ ] Web 管理与 Monitor 面板（`:9820`）能正常打开并显示两个项目在线
- [ ] 主人 QQ 私聊发送文本，小鲸鱼正常短句回复且无多余状态栏
- [ ] 主人私聊发送一张图片，小鲸鱼能正确识别图片内容
- [ ] 触发一次表情包发送，验证 `cc-connect send --image stickers/...` 正常发出图片
- [ ] 白名单群聊内 `@` 小鲸鱼及普通聊天响应正常，非白名单群静默忽略
