# AutoClip Go · Web 视频切片服务

Go + React 实现的独立 Web 版本，复用 AutoClip 的剪辑器界面。支持视频导入、
本地语音转写、AI 内容分析、手动剪辑和 MP4 导出；通过 Docker 启动两个服务，
不需要桌面客户端、Python 业务服务、Redis 或 Celery。

> **安全边界：仅用于本机或可信 LAN/VPN。没有登录、用户隔离和费用配额。**
> 所有访问者共享项目、模型设置、Cookie 和模型调用费用，不要直接开放公网。
> 请只处理自己有权使用的视频，不会自动导入原版桌面端的项目或密钥。

文档更新：2026-09-29。

## 1. 功能概览

| 功能 | 当前实现 | 是否需要配置模型 |
| --- | --- | --- |
| 视频导入 | 上传本地视频，可附 UTF-8 SRT；或填写 Bilibili / YouTube 视频页面链接 | 否；部分链接需要 Cookie |
| 本地转写 | 无字幕且有音轨时，使用 CPU whisper.cpp + multilingual base 模型 | 否，Docker 镜像内置 |
| 手动剪辑 | 新建草稿，调整片段时间、顺序、标题、画幅和音轨，组合片段 | 否 |
| 字幕内容分析 | 大纲 → 时间线 → 评分 → 标题 → 聚类 → 草稿 | 文本模型 |
| 视觉分析 | 抽样图片识别事件、复核候选并生成草稿，不是逐帧分析 | 视觉理解模型，需明确允许发送图片 |
| AI 改写 / 翻译 | 改写标题和标签；非原语言导出会翻译相关文字与字幕 | 文本模型 |
| 标题和字幕 | 9 种标题样式、中文字体、长字幕换行、标题预览 | 否，手动编辑不调用模型 |
| MP4 导出 | H.264 / 可选 AAC，30 fps；原画幅、横屏、竖屏 | 原语言导出不需要模型 |
| 任务管理 | 独立进度、刷新恢复、取消、显式重试、已完成成片下载 | 否 |

**导入完成 ≠ AI 分析完成 ≠ MP4 导出完成。** 导入不会自动发起付费分析，
AI 生成草稿后也需要单独点击导出。

## 2. 启动流程

### 2.1 环境要求

- 当前 Docker 配置仅支持 **Linux amd64** 容器。
- Docker Engine 23+（BuildKit）及 Compose v2 或兼容新版。
- Windows 使用 Docker Desktop 的 WSL2 / Linux 容器后端；组件安装后如提示重启，
  请先保存工作、重启电脑，再打开 Docker Desktop。
- 建议至少 4 核 CPU / 8 GiB 内存，并为原片、模型和渲染临时文件预留磁盘空间。
  这是部署建议，不是已完成的最低配置性能保证；数据不要放在 NFS 上。
- 首次构建需要访问 Docker Hub、Debian、Go/npm、GitHub、Hugging Face。
  单纯使用已导入的视频进行本地手动剪辑，不需要云模型凭据。

先确认引擎已启动，而不只是安装了命令行：

```sh
docker info
docker compose version
```

### 2.2 获取代码和首次配置

新机器使用 HTTPS 获取源码（已有项目目录就跳过 clone）：

```sh
git clone https://github.com/zylar06/goclip.git autoclip-go
cd autoclip-go
```

Windows 本机已有目录：

```powershell
cd D:\com\autoclip-go
if (-not (Test-Path -LiteralPath .env)) { Copy-Item -LiteralPath .env.example -Destination .env }
```

Linux / Bash，在项目根目录执行：

```sh
if [ ! -f .env ]; then cp .env.example .env; fi
```

`.env` 可不创建，此时使用默认值；创建后只修改需要的配置，**不要覆盖已有文件**。
模型 API Key 现在可直接填写到 `.env`，配置方法见第 3 节。真实密钥不要写入 `.env.example`。

### 2.3 构建并启动

以下命令在项目根目录执行，PowerShell / Bash 均可使用：

```sh
docker compose config --quiet
docker compose up -d --build --wait --wait-timeout 180
docker compose ps
```

`--wait-timeout 180` 是等待服务就绪的限制，**不是整个镜像构建的超时**。
`web` 和 `worker` 都显示 `healthy` 后，在本机浏览器打开：

```text
http://127.0.0.1:8080
```

可用 PowerShell 检查 API：

```powershell
Invoke-RestMethod -Uri http://127.0.0.1:8080/api/v1/health
```

健康接口返回 `status: ok` 只证明 API/数据库可用；worker 健康状态仍需看
`docker compose ps`，模型是否可调用需要单独测试。

### 2.4 后续启动、停止和更新

| 场景 | 在项目根目录执行 |
| --- | --- |
| 已构建、代码没变化 | `docker compose up -d --no-build --wait --wait-timeout 180` |
| 停止服务，保留容器和数据 | `docker compose stop` |
| 启动已停止的容器 | `docker compose start` |
| 查看状态 | `docker compose ps` |
| 查看近期日志 | `docker compose logs --tail=100 web worker` |
| 持续查看日志，Ctrl+C 退出日志查看 | `docker compose logs -f --tail=100 web worker` |
| 更新代码后重新构建并启动 | `docker compose up -d --build --wait --wait-timeout 180` |
| 修改端口或运行限制后应用配置 | `docker compose up -d --no-build --wait --wait-timeout 180` |

更新代码前先备份并处理本地未提交修改，再拉取所需版本，不要用强制覆盖来更新。
变更构建源后需要重新 `--build`；修改 `.env` 后仅 `restart` 不会应用新容器配置。
**不要执行 `docker compose down -v` 来更新或排障，它会删除项目和模型数据卷。**

### 2.5 构建速度与缓存

镜像包含 FFmpeg、yt-dlp、Deno、whisper.cpp、base 模型和字体，因此第一次构建
比直接运行预构建镜像耗时。当前配置做了以下优化：

- 默认使用 Debian 官方 HTTPS CDN，不再默认访问较慢的历史快照。
- BuildKit 缓存 APT 安装包、npm 包、Go 模块及编译结果。
- 只有 `web` 负责构建；`worker` 复用相同镜像，不重复提交构建。
- 使用 Docker 自带的 Dockerfile 解析器，避免额外拉取解析器镜像。
- 保留依赖签名、固定工具/模型哈希验证和构建内 Go / 前端测试。

2026-09-29 本机实测：旧快照构建在 **48 分 33 秒**后取消；优化后构建完成用时
**13 分钟**（复用了已拉取的基础镜像），最终一次后端增量构建及健康启动为
**31.7 秒**。这些不是完全冷缓存或所有网络环境的耗时保证。

卡住时先检查日志中的实际步骤；不要重复启动多个构建，也不要清理缓存“加速”。
旧终端仍执行 `apt-snapshot.sh` 时不会自动切换新配置，应在该终端停止旧构建后
再启动新版。APT 的 30 秒无响应限制及 2 次重试不是构建总时限。

## 3. 配置方法

### 3.1 配置放在哪里

| 配置类型 | 修改位置 | 生效方式 |
| --- | --- | --- |
| 文本 / 视觉模型、Base URL、API Key（推荐） | 项目根目录 `.env` | 重新创建容器配置，web 启动时写入加密数据库 |
| 临时修改模型 / 查看配置 | 网页顶部“设置 / Settings” | 保存后生效；下次 web 启动会重新应用非空环境配置 |
| B 站 / YouTube Cookie | 网页设置页的“导入 Cookie” | 上传后用于后续链接导入 |
| 访问 IP、端口、时长/大小/超时限制 | 项目根目录 `.env` | 重新执行 `docker compose up -d --no-build` |
| Debian 构建源和快照日期 | `.env` 中的构建参数 | 重新执行 `docker compose up -d --build` |
| 原生开发的工具路径和数据目录 | 启动进程的环境变量 | 重启对应本地进程 |

### 3.2 在 `.env` 配置模型和密钥（推荐）

在项目根目录 `.env` 填写以下六项。示例只展示格式，地址和模型 ID 请使用自己有权限的
兼容服务，将占位密钥替换为实际值，不要把占位示例直接当作可用配置：

```dotenv
AUTOCLIP_TEXT_BASE_URL='https://provider.example/v1'
AUTOCLIP_TEXT_MODEL='your-text-model'
AUTOCLIP_TEXT_API_KEY='replace-with-your-text-key'
AUTOCLIP_VISION_BASE_URL='https://provider.example/v1'
AUTOCLIP_VISION_MODEL='your-vision-model'
AUTOCLIP_VISION_API_KEY='replace-with-your-vision-key'
```

每组 Base URL 和 Model 必须一起填写；Key 可留空以连接无需鉴权的本地服务。
只使用文本模型时，视觉组三项全部留空即可。推荐用单引号包住值，避免 Key 中的 `$`、`#`
被 Compose 插值或解释；值中若包含单引号，按 Compose `.env` 语法写成 `\'`。

首次升级到支持此配置的版本，执行：

```sh
docker compose up -d --build --wait --wait-timeout 180
```

之后只改 `.env` 时，执行 `docker compose up -d --no-build --wait --wait-timeout 180`。
不要仅用 `docker compose restart` 期待加载新环境值。等待任务结束后再重启，避免中断工作。

**配置优先级与安全规则：**

- Compose 优先使用同名 shell 环境变量，其次使用 `.env`；若值不符合预期，检查终端是否设置了同名变量。
- web 启动时，任意非空的模型组完整替换该类数据库配置；两组都校验通过后才在一个事务中保存。
- 三项全空则不改该类数据库设置；删除 `.env` 字段**不会清除以前已保存的 Key**。
- 完整环境组里的空 Key 表示**无鉴权**，不会复用数据库旧 Key，避免向新地址泄露旧密钥。
- 网页仍可查看和修改，保存立即生效；但下次 web 启动会重新应用 `.env`。以 `.env` 为长期配置来源。
- worker 只读取共享加密数据库，不在启动时重复导入；健康检查、保存和启动都不调用模型。
- 字段不完整或不合法时启动明确失败，不会忽略错误或偷偷混用旧值。
- `.env` 本身是明文密钥文件；限制文件访问、不要提交 Git。密钥不会加入 Docker build args 或镜像，
  但有 Docker 管理权限的人可查看容器环境。不要分享完整 `docker compose config` / `docker inspect` 输出。

可以进入网页 `/settings` 确认模型名和配置状态；若点击“测试”，会真正发送请求，可能计费。

### 3.3 网页配置、接口格式与密钥

1. 打开网页顶部 **设置 / Settings**（路径 `/settings`）。
2. 在 **文本模型 / Text model** 填写以下三项：

   | 字段 | 如何填写 |
   | --- | --- |
   | Base URL | 服务商的 **Chat Completions 兼容接口基础地址**，例如 `https://provider.example/v1`；这是占位示例，不可直接使用 |
   | Model name | 服务商控制台中自己有权限使用的准确模型 ID，不是随意填写的显示名 |
   | API Key | 自己的密钥，可在 `.env` 配置或在此临时修改；不提交到 Git，也不要发到聊天或日志中 |

3. 点击 **保存文本设置 / Save text settings**。
4. 确认可以产生少量调用费用后，点击 **测试已保存的文本设置**。

**先保存，再测试。** 测试使用服务端已保存的配置，不会测试尚未保存的表单值。
保存本身不调用模型；测试会真正发送请求，可能计费。

接口约定：

- 当前实现使用兼容的 `/chat/completions` 协议，不支持直接填写其他协议的原生端点。
- 基础地址若是 `https://provider.example/v1`，程序会追加 `/chat/completions`；
  若只填写域名根地址，则追加 `/v1/chat/completions`。
- 可填写服务商明确提供的其他兼容前缀，例如 `/compatible-mode/v1`。
  **不要直接填写完整 `/chat/completions` 地址**，也不要在 URL 中放 Key、查询参数或账号密码。
- 具体基础地址、模型 ID 和账号权限以自己的服务商为准；当前仓库没有自动发现模型列表。
- 已保存 Key 后，密钥框留空表示**保留旧密钥，不是删除**；换密钥时重新输入。
  更换服务端 origin（协议、主机或端口）且已有密钥时，需要显式重新填写 Key。
- 无需鉴权的本地兼容服务可首次保存空 Key；不代表能用空值清除已有密钥。

Key 在服务端数据库中加密保存，页面不回传明文，也不写入浏览器 localStorage。
加密依赖 `data` 卷里的 `master.key`：**备份数据库时必须一起备份它**。
有权读取完整数据卷的人仍可访问解密所需材料，数据库加密不是用户隔离。

### 3.4 视觉模型

在 `.env` 配置 `AUTOCLIP_VISION_*`，或在同一设置页的 **视觉模型 / Vision model**
中独立填写 Base URL、模型 ID 和 Key，然后单独保存、测试。
需要的是**支持图片输入的视觉理解模型**，不是图片生成模型。

文本和视觉配置可以使用同一供应商，但不会自动共用或自动回退。
视觉测试会发送一张生成的测试图片；项目视觉分析还需明确勾选允许发送视频抽样图。
仅做字幕分析/手动剪辑时，不必配置视觉模型。

使用自建模型时，调用来自 Go 服务所在的机器/容器，而不是浏览器。
容器中的 `127.0.0.1` 是容器自身，不能直接当作 Windows 宿主机地址；请填写
**容器可达的可信服务地址**，并验证监听地址、端口及防火墙，不要为此把应用开放到公网。

### 3.5 Bilibili / YouTube Cookie

公开视频可以先不配 Cookie；平台要求登录时，在设置页上传自己的
**Netscape 格式 `cookies.txt`**，文件需非空且不超过 1 MiB。
支持从设置页删除已保存 Cookie；不支持自动读取宿主机浏览器登录状态。

Cookie 与模型配置一样由所有服务访问者共享。不要上传他人 Cookie，不要提交 Cookie
文件到仓库。Cookie 不能保证绕过平台风控、地区限制或视频访问权限。

链接请填写完整 HTTPS 视频页面地址，不使用 `b23.tv` 等短链接。
B 站下载会从可用普通 CDN 格式中选择音视频并避开 MCDN，码率/编码可能不同；
没有适用组合时明确失败，不会静默丢掉音轨。

### 3.6 `.env` 参数表

以下默认值与 `.env.example` / `compose.yaml` 对应：

| 变量 | 默认值 | 用途 |
| --- | --- | --- |
| `BIND_IP` | `127.0.0.1` | 宿主机监听 IP，默认仅本机可访问 |
| `PORT` | `8080` | 宿主机访问端口；容器内仍为 8080 |
| `MAX_UPLOAD_BYTES` | `4294967296` | 默认 4 GiB 文件大小限制；不是整个数据卷容量配额 |
| `MAX_VIDEO_SECONDS` | `7200` | 默认视频时长上限 2 小时 |
| `TASK_TIMEOUT_SECONDS` | `21600` | 单任务总超时默认 6 小时；原生工具/模型请求另有较短超时 |
| `AUTOCLIP_TEXT_BASE_URL` / `AUTOCLIP_TEXT_MODEL` / `AUTOCLIP_TEXT_API_KEY` | 全空 | 文本模型基础地址、模型 ID、密钥；三项全空保留数据库配置 |
| `AUTOCLIP_VISION_BASE_URL` / `AUTOCLIP_VISION_MODEL` / `AUTOCLIP_VISION_API_KEY` | 全空 | 独立视觉模型配置；不自动复用文本模型密钥 |
| `APT_SOURCE_MODE` | `mirror` | `mirror` 使用当前软件源；`snapshot` 使用历史快照 |
| `DEBIAN_MIRROR` | `https://deb.debian.org/debian` | mirror 模式的主软件源 |
| `DEBIAN_SECURITY_MIRROR` | `https://deb.debian.org/debian-security` | mirror 模式的安全更新源 |
| `DEBIAN_SNAPSHOT` | `20260901T000000Z` | snapshot 模式的固定快照日期 |

三个资源限制必须为正整数。提高限制并不会增加磁盘或内存容量。
mirror 模式不固定 APT 包版本；基础镜像 digest 和工具/模型哈希仍固定。
自定义软件源只能使用可信 HTTP(S) 地址，不得包含凭据、查询参数或注入内容；不会自动切换备用源。

例如只想把本机访问端口改成 18080，在已有 `.env` 中修改：

```dotenv
BIND_IP=127.0.0.1
PORT=18080
```

再执行 `docker compose up -d --no-build`，浏览器改用 `http://127.0.0.1:18080`。
若需要局域网访问，`BIND_IP` 填服务器实际的 LAN/VPN IP，客户端使用该 IP 访问；
确保只有可信网络能连接。不要使用公网 IP，也不要把 `0.0.0.0` 当作安全访问控制。

**注意：`.env` 由 Compose 读取，不会自动全部传入容器。** 只有 Compose 引用的
变量生效；现在显式支持上面的六个 `AUTOCLIP_TEXT_*` / `AUTOCLIP_VISION_*` 字段，
但任意其他供应商 Key 变量或 `FFMPEG_PATH` 不会自动映射。
原生 `go run` 进程也不会自动加载 `.env`。

## 4. 使用流程

### 4.1 不使用云模型：手动剪辑

1. 在项目首页上传视频（可附已校对的 SRT），或提交视频链接。
2. 等待导入完成；无字幕且有音轨时自动进行本地 CPU 转写。
3. 在项目页点击 **新建手动草稿 / New manual draft**，不需要启动 AI 分析。
4. 在编辑器设置镜头起止时间、顺序、标题、画幅、字幕和原声音轨。
5. 输出语言选择 **原语言 / Source**，避免导出阶段调用文本模型翻译。
6. 保存草稿，点击导出，等待任务 `completed`，再下载 MP4。

### 4.2 使用 AI 生成草稿

1. 先保存并测试所需模型，再导入视频。
2. 在项目页选择字幕分析或视觉分析，设置内容/精彩片段/推广目标、
   目标时长（界面为 10–120 秒）、画幅、语言及可选指令。
3. 检查费用与数据发送提示，确认后开始；视觉分析还需允许抽样图片上传。
4. 等待候选和草稿生成，人工检查时间边界、标题和内容；可选片段组成合辑。
5. 保存编辑并导出。选择非原语言会在导出阶段调用文本模型翻译，可能产生额外费用。

### 4.3 输出与质量边界

- 原画幅保持比例、不放大、最长边上限 1920；横屏 1920×1080，竖屏 1080×1920。
- 标题支持 plain、impact、card、comic、neon、arena、editorial、pixel、frosted，
  默认只在成片开头 4 秒内显示。
- 新增字幕使用项目附带字体并按字宽换行；无法显示的字符或过高字幕会明确报错。
  需要校对原始 SRT 时，应在导入前编辑好，当前项目页字幕列表不是全文编辑器。
- Whisper 仍可能识别错词或人名。原片已有硬字幕时，新增字幕会叠加，可关闭新增字幕；
  不支持自动移除原片硬字幕。
- 原片编码可能不被浏览器支持，导入成功不代表原片能在每个浏览器预览；最终导出为 H.264/AAC MP4。
- 当前没有 GPU、自动发布、定时任务、多机队列或多租户能力。视觉边界来自抽样估计，需人工复核。

## 5. 功能架构

```text
浏览器：React + TypeScript + 剪辑器
       │ 同源 HTTP /api/v1 + SSE 任务事件
       ▼
web：Go HTTP API + 前端静态资源 + 设置/项目/草稿接口
       │ 写任务、保存草稿、读取状态
       ▼
共享 data 卷：SQLite WAL + 加密设置 + 原片 + 字幕 + 检查点 + 导出
       ▲
       │ 领取任务、写心跳、更新进度和结果
worker：Go 后台任务执行器（全局一个重任务）
       ├─ 导入：yt-dlp + FFmpeg/ffprobe
       ├─ 转写：whisper.cpp ← models 卷的 ggml-base.bin
       ├─ 分析：字幕文本 / 抽样图片 → 已配置的兼容模型服务
       └─ 导出：Go 标题 PNG + 字幕 + FFmpeg → 校验 → 发布 MP4
```

两个服务使用**同一份 Docker 镜像**，分别执行 `autoclip web` 和 `autoclip worker`。
worker 等待 web 健康后启动；只有 web 向宿主机映射 HTTP 端口。
SQLite 同时承担持久状态和任务队列，不需要 Redis、Celery 或额外数据库服务。

### 5.1 源码结构

| 路径 | 职责 |
| --- | --- |
| `cmd/autoclip/` | web / worker / healthcheck 入口及环境变量 |
| `internal/domain/` | 项目、任务、草稿、模型设置与校验 |
| `internal/store/` | SQLite、任务领取/恢复、草稿版本、密钥加密 |
| `internal/httpapi/` | API、设置、文件上传、SSE 和 Range 下载 |
| `internal/worker/` | 导入、分析、翻译和导出的任务编排 |
| `internal/media/` | 下载、探测、转写、抽帧、标题、字幕和 FFmpeg 渲染 |
| `internal/ai/` | 兼容接口客户端、文本/视觉分析、改写、翻译 |
| `web/` | React 项目页、设置页、剪辑器与生成的接口类型 |
| `api/openapi.json` | API 契约；由 `cmd/specgen/` 生成 |
| `assets/fonts/` | 随项目分发的字体、许可证和校验清单 |
| `Dockerfile` / `compose.yaml` | 镜像构建、共享卷、端口、安全限制和健康检查 |
| `tools.lock.json` | 基础镜像、原生工具和模型版本/校验信息 |
| `scripts/` | 配置回归测试与本地/容器冒烟测试 |
| `doc_auto/` | 模块设计、运维说明与实际验收记录 |

### 5.2 任务、费用和数据安全

启动配置路径：`.env` → Compose 运行时环境 → web 校验并原子写入加密设置 →
web / worker 从 SQLite 读取。程序不会直接解析 `.env`，也不会把密钥编进二进制或镜像。

- SQLite 是状态唯一权威，文件存在不等于任务成功。
- 草稿使用修订号避免覆盖并发编辑；导出基于提交时的不可变草稿快照。
- 任务状态为 queued / running / completed / failed / interrupted / cancelled。
  取消请求需等待底层进程退出；服务恢复时过期任务标记为 interrupted。
- 不自动重放付费请求，不会在文本模型失败后偷偷改用视觉模型。重试必须显式确认，
  **上一次超时调用可能已计费**；可复用的已完成阶段通过校验后才重用。
- 模型调用发生在服务端。字幕分析发送文本；视觉分析发送授权的抽样图片；
  本地转写和原语言手动导出不调用云模型。

## 6. 数据持久化与备份

Compose 默认创建 `autoclip-go_data`、`autoclip-go_models` 两个命名卷：

| 卷 / 容器路径 | 内容 |
| --- | --- |
| `data` → `/data` | SQLite/WAL、`master.key`、加密的 Key/Cookie、原片、字幕、草稿/任务数据、检查点、导出 |
| `models` → `/models` | 本地 Whisper 模型 |

不是把视频直接写入宿主机项目目录的 `data/`；原生开发模式才默认使用本地 `./data`。
`docker compose stop` 和不带 `-v` 的 `down` 保留命名卷；删除卷则会丢失数据。

一致性备份流程：停止两个服务 → 用可信卷备份工具完整备份两个卷（包含 SQLite、
WAL/SHM、主密钥及权限）→ 加密保存备份 → `docker compose start` 并检查健康状态。
不要在运行时只复制一个 SQLite 文件，也不要把 `master.key` 与数据库拆开恢复。
模型升级时已有 `models` 卷不会因镜像更新自动替换其中的旧文件，应单独验证/迁移，
不要为更新模型删除含项目数据的卷。更多恢复边界见 `doc_auto/operations.md`。

源码上传 Git 时只提交源码、文档、锁文件和 Docker 配置；**不需要把 Docker 镜像放进 Git**。
要分发预构建镜像应另用镜像仓库。`.env`、`data/`、`artifacts/` 已被忽略，
但任意位置的 `cookies.txt` 不一定被忽略，提交前仍需检查 `git status` 和差异。

## 7. 常见问题

| 现象 | 排查方向 |
| --- | --- |
| `docker info` 无 Server / 找不到 engine pipe | Docker Desktop 未就绪，或 WSL2/系统组件启用后尚未重启 |
| 构建一直下载系统包 | 检查实际源和网络；确认使用新 `apt-setup.sh`；看日志，不重复建、不清缓存 |
| Docker Hub 认证地址连接失败 | 检查 Docker 的网络/代理；缓存优化不绕过基础镜像仓库访问 |
| 网页打不开 | 检查两个服务状态、端口占用、`.env`；换端口后 URL 也要改变 |
| 能打开网页但任务一直排队 | 看 worker 健康和日志；当前只允许一个重任务 |
| 模型测试 401/403 | 检查已保存 Key、账号权限、服务商限制 |
| 模型测试 404 / endpoint 错误 | 检查基础地址与协议，不要填完整 completions 路径；同时核对模型 ID |
| 修改表单后测试仍用旧配置 | 先保存，测试只读服务端已保存配置 |
| B 站/YouTube 导入失败 | 检查完整视频链接、授权 Cookie、平台限制和实际下载错误；不要无休止重试 |
| 转写慢 / 字幕有错词 | 当前使用 CPU base 模型，检查转写进度；发布前人工校对 |
| 原片不能预览但能导出 | 浏览器不支持原片编码；可换兼容 MP4 输入，最终输出使用 H.264/AAC |
| 字幕重复或遮住原字幕 | 关闭新增字幕；原片硬字幕不会自动移除 |
| AI 完成但没有成片 | 生成草稿后仍需导出，直到任务 completed 且 MP4 可下载 |

分享日志前请移除凭据、Cookie、签名下载链接和私密内容。

## 8. 本地开发与验证

非 Docker 开发需要 Go（`go.mod` 要求 1.25.0+）、Node.js（构建使用 Node 22）、
FFmpeg/ffprobe、yt-dlp、Deno、whisper.cpp CLI 及兼容 base 模型。
工具版本和哈希以 `tools.lock.json` 为准；`whisper-cli` 不是 Python Whisper 命令。

在根目录构建前端，再启动服务：

```sh
cd web
npm ci
npm run typecheck
npm test
npm run build
cd ..
go run -buildvcs=false ./cmd/autoclip web
```

另一个终端进入**同一根目录**并使用相同数据目录：

```sh
go run -buildvcs=false ./cmd/autoclip worker
```

若 Docker 正在占用 8080，本地 web 改用另一个 `AUTOCLIP_ADDR`，或先主动停止 Docker
服务。不要让本地测试进程连接生产数据目录。

| 原生进程环境变量 | 默认值 |
| --- | --- |
| `AUTOCLIP_ADDR` | `127.0.0.1:8080` |
| `AUTOCLIP_DATA_DIR` | `data` |
| `AUTOCLIP_WEB_DIR` | `web/dist` |
| `FFMPEG_PATH` / `FFPROBE_PATH` | `ffmpeg` / `ffprobe` |
| `YTDLP_PATH` / `WHISPER_PATH` | `yt-dlp` / `whisper-cli` |
| `WHISPER_MODEL` | `models/ggml-base.bin` |
| `FONT_DIR` | `assets/fonts` |

PowerShell 用 `$env:变量名='值'`，Bash 用 `export 变量名='值'`；工具路径可用绝对路径。
web 和 worker 应保持数据目录及资源限制配置一致。原生模式可给 web 设置相同的六个
`AUTOCLIP_TEXT_*` / `AUTOCLIP_VISION_*` 环境变量来导入模型配置；不会自动读取 `.env`。
前端热更新可在 `web/` 运行 `npm run dev`，API 默认代理到 8080；本地 API 换端口时
同步设置 Vite 进程的 `AUTOCLIP_API_ORIGIN`。仅启动 Vite 不会启动 Go worker。

测试与契约检查：

```sh
go test -mod=readonly -timeout 180s ./...
go vet -mod=readonly ./...
node --test scripts/apt-setup.test.mjs scripts/deployment.test.mjs
cd web
npm run typecheck
npm test
npm run build
```

修改 API 后在根目录执行 `go run ./cmd/specgen`，再在 `web/` 执行
`npm run generate:api`，提交对应的契约和生成类型。原生媒体测试所需变量、可选 ASR
测试及独立容器冒烟流程见 `doc_auto/verification.md`；缺少原生依赖时的跳过不等于验收通过。

## 9. 已验证范围与文档索引

2026-09-29 本机记录：Go **254** 项（含子测试）通过、0 失败/跳过，`go vet` 通过；
前端 **43/43**，构建配置 **13/13**；媒体修复后实际运行镜像内的媒体测试 **66/66**。
指定 B 站视频 `BV1TRhs6hEQp` 完成真实下载、本地转写及 30 秒手动切片导出，
验证了视频/音轨、HTTP 下载、中文字体及字幕换行。

**不代表所有外部服务均已验收：**未调用付费文本/视觉模型验证自动选片；
YouTube/其他受限视频、目标服务器、完整 race detector 和字幕识别准确率仍有独立验收边界。
历史测试证据在本机被 Git 忽略的 `artifacts/`，克隆仓库不会附带这些日志、原片或成片。

- `doc_auto/architecture.md`：模块职责、API 契约、状态和可靠性边界。
- `doc_auto/operations.md`：构建、故障、持久化、备份与恢复。
- `doc_auto/ai.md`：模型协议、分析流水线、费用与重试。
- `doc_auto/media.md`：转写、字幕、字体、采样及渲染。
- `doc_auto/frontend.md`：Web 界面及前端契约。
- `doc_auto/verification.md`：实际测试、指定视频验收和未完成验收项。

源码采用 MIT；FFmpeg、字体等组件各有许可证，见 `THIRD_PARTY_NOTICES.md`
及 `assets/fonts/` 的许可证文件。
