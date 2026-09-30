# GoClip

长视频输入，生成可编辑的精彩高光切片。

上传本地视频，或粘贴 B 站 / YouTube 链接。GoClip 下载素材、读取或转写字幕、分析画面、合并证据、自动构图，并导出 MP4。视频处理在本机 Docker 容器中完成；文本和视觉分析使用你配置的模型服务。

> 适合本机或可信内网使用。实例内的项目、模型配置、Cookie 和任务费用由访问者共享。

## 当前功能

| 能力 | 结果 |
| --- | --- |
| 高光切片 | 同时分析字幕和抽样画面，合并重叠证据后生成候选草稿。 |
| 场景规则 | 选择足球进球、篮球回合、游戏高光等预设，也可以直接编辑规则。 |
| 竖屏自动构图 | 人脸跟踪生成 9:16 裁切路径，检测不到人脸时使用居中裁切。 |
| 本地媒体处理 | 使用 yt-dlp、FFmpeg、Whisper、MediaPipe 和 OpenCV。 |
| 手动剪辑 | 调整片段时间、标题、字幕、画幅、构图和取景位置。 |

## 处理流程

    导入视频
      → 获取平台字幕；缺失时用本地 Whisper 转写
      → 每秒抽取一帧，长视频按每批 60 帧串行处理
      → 文本模型分析字幕，视觉模型分析画面
      → 本地合并两类候选和证据
      → 生成高光草稿
      → 可选竖屏构图与 MP4 导出

文本模型从字幕中定位事件、边界和观看价值。视觉模型只接收获得授权的抽样帧，负责识别可见事件、画面结果和支持帧。GoClip 在本地按时间重叠合并两路结果，并提高同时得到两类证据的候选分数。

没有可用字幕时，系统继续运行视觉路径。没有音频时，系统直接使用画面证据。选择 9:16 时，GoClip 对视频帧进行本地人脸检测和跟踪，生成动态裁切路径。视觉模型负责判断哪些画面是高光，不负责逐帧裁切坐标。

| 阶段 | 输入 | 输出 |
| --- | --- | --- | --- |
| 字幕准备 | 平台字幕、上传字幕或本地 Whisper | 带时间戳的字幕 |
| 画面采样 | 视频、FFmpeg、1 秒间隔 | 每批最多 60 张静帧 |
| 双路分析 | 字幕和静帧 | 文本候选、视觉候选 |
| 证据融合 | 两路候选 | 排序后的高光候选 |
| 草稿与导出 | 候选、画幅、字幕设置 | 可编辑草稿和 MP4 |

新项目使用高光融合流程。旧草稿仍可读取和编辑。

## 快速开始

### 1. 安装 Docker

安装并启动 Docker Desktop。

- macOS 默认使用 Linux containers。
- Windows 在 Docker Desktop 菜单中选择 Switch to Linux containers。
- Linux 安装 Docker Engine 和 Docker Compose 插件。

确认容器类型：

~~~sh
docker info --format '{{.OSType}}'
~~~

输出 linux 后即可继续。

### 2. 启动服务

~~~sh
git clone https://github.com/zylar06/goclip.git
cd goclip
docker compose up -d --build --wait --wait-timeout 180
~~~

首次构建会下载镜像、媒体工具、Whisper 模型和 Python 引擎。完成后打开：

<http://127.0.0.1:8080>

确认服务状态：

~~~sh
docker compose ps
~~~

web 和 worker 都显示 healthy 后即可使用。

### 3. 配置模型

打开网页的 设置，分别填写文本模型和视觉模型。

| 配置 | 用于 |
| --- | --- |
| 文本模型 | 字幕分析、评分、标题和推广文案。 |
| 视觉模型 | 抽样画面事件分析和视觉高光。 |

填写 OpenAI Chat Completions 兼容接口的 Base URL、模型 ID 和 API Key。Base URL 填接口根地址，不要追加 /chat/completions。保存后分别执行连接测试。

也可以在根目录的 .env 配置：

~~~dotenv
AUTOCLIP_TEXT_BASE_URL=https://example.com/compatible-mode/v1
AUTOCLIP_TEXT_MODEL=your-text-model
AUTOCLIP_TEXT_API_KEY=your-text-api-key
AUTOCLIP_VISION_BASE_URL=https://example.com/compatible-mode/v1
AUTOCLIP_VISION_MODEL=your-vision-model
AUTOCLIP_VISION_API_KEY=your-vision-api-key
~~~

不要提交 .env、API Key 或 Cookie。

### 4. 制作第一条高光切片

1. 上传视频，或粘贴 B 站 / YouTube 链接。
2. 等待导入任务完成。
3. 在制作方案中选择高光场景。
4. 修改场景规则、目标时长和画幅。
5. 确认模型费用，并授权上传抽样图片。
6. 点击确认并开始制作。
7. 打开草稿，检查片段、标题、画幅和字幕。
8. 保存草稿并导出。

高光分析完成后默认生成可编辑草稿。制作方案可以启用一键出片，也可以先调整片段边界和画幅再导出。

可用的场景预设包括通用精彩片段、足球进球与关键进攻、篮球得分与关键回合、游戏击杀与胜利、演讲与知识重点、产品演示关键步骤。规则会同时传给文本模型和视觉模型。你可以直接写入具体目标，例如：

```text
重点寻找进球、射门、门将扑救、关键传球和进球庆祝；排除长时间控球、暂停和无关回放。
```

视觉分析只发送本次任务授权的抽样图片。字幕内容发送给已保存的文本模型。模型调用可能产生费用。

## 实现流程

详细源码设计见 [架构与源码流程](doc_auto/architecture.md)。运行时的主链路如下：

```text
React 网页
   │ HTTP / SSE
   ▼
Go Web ── SQLite（项目、计划、任务、草稿、设置）
   │
   ▼
Go Worker
   ├─ yt-dlp / ffprobe / FFmpeg：下载、探测、抽帧、预览、导出
   ├─ whisper.cpp：缺字幕时本地转写
   ├─ internal/ai：文本和视觉模型适配、候选评分、证据融合和检查点
   └─ Python engine：MediaPipe/OpenCV 人脸跟踪、构图和动态导出
```

一次高光任务按以下阶段执行：

1. Web API 创建项目、导入任务和制作方案；任务状态通过 SSE 推送到页面。
2. Worker 用 yt-dlp、FFmpeg 和 ffprobe 下载并校验素材。
3. 确认制作后读取平台字幕；缺少字幕时运行本地 Whisper。
4. Worker 按 1 秒间隔抽帧，每批最多 60 帧，依次调用视觉模型。
5. 文本模型处理字幕，视觉模型处理静帧；两路候选由 Go 在本地按时间重叠合并。
6. 成功候选写入草稿。选择 9:16 时，Python engine 调用 MediaPipe/OpenCV 跟踪人脸，生成动态裁切路径。
7. 导出任务读取草稿 revision 的不可变快照，用 Python engine 和 FFmpeg 生成并校验 MP4。
8. Worker 保存导出记录和检查点；失败重试会复用已完成阶段，不重复已发布的结果。

任务会保存候选、选择理由和检查点。重试时可以复用已完成阶段。旧草稿仍可读取和编辑；旧分析模式只用于兼容历史数据。

## B 站导入

B 站可能返回 HTTP 412。登录 B 站后，用 Cookie-Editor 导出 Netscape 格式的 cookies.txt，再到 设置 → 导入 Cookie 上传。

Cookie 等同于登录凭据。只上传自己的 Cookie，过期后重新导出。

## 常用命令

| 操作 | 命令 |
| --- | --- |
| 查看状态 | docker compose ps |
| 查看日志 | docker compose logs --tail=100 web worker |
| 停止服务并保留数据 | docker compose stop |
| 再次启动 | docker compose up -d |
| 重新构建并启动 | docker compose up -d --build --wait --wait-timeout 180 |
| 删除容器并保留数据 | docker compose down |

Docker 数据卷保存视频、草稿、设置和模型。docker compose down -v 会删除这些数据。

## 文档

- [架构与源码流程](doc_auto/architecture.md)
- [媒体下载、转写和导出](doc_auto/media.md)
- [部署、备份和恢复](doc_auto/operations.md)
- [OpenAPI](api/openapi.json)

## 目录

~~~text
cmd/autoclip/       服务入口和健康检查
internal/httpapi/   HTTP API
internal/worker/    导入、分析和导出任务
internal/media/     yt-dlp、FFmpeg、Whisper 和抽帧
internal/ai/        文本分析、视觉分析、提示词和检查点
internal/engine/    Go 与 Python engine 的进程桥接
engine/              Python 自动构图和导出引擎
web/                 React 前端和草稿编辑器
~~~

## 开发

~~~sh
go test ./...

cd web
npm ci
npm run typecheck
npm test
~~~

## 安全

GoClip 没有用户登录和隔离机制。部署到局域网前，请用防火墙限制端口，并保护 API Key 和 B 站 Cookie。只下载和处理你有权使用的视频。
