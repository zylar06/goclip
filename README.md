# GoClip

长视频输入，生成可编辑的内容切片、精彩高光和推广短片。

上传本地视频，或粘贴 B 站 / YouTube 链接。GoClip 下载素材、读取或转写字幕、分析候选片段、自动构图，并导出 MP4。视频处理在本机 Docker 容器中完成；文本和视觉分析使用你配置的模型服务。

> 适合本机或可信内网使用。实例内的项目、模型配置、Cookie 和任务费用由访问者共享。

## 它能做什么

| 能力 | 结果 |
| --- | --- |
| 内容切片 | 从字幕中拆出语义完整的内容片段，自动导出 MP4。 |
| 精彩高光 | 从字幕或画面中找出最值得观看的片段，生成可编辑草稿。 |
| 推广成片 | 为候选片段生成标题、Hook 和发布用草稿。 |
| 竖屏自动构图 | 人脸跟踪生成 9:16 裁切路径，检测不到人脸时使用居中裁切。 |
| 本地媒体处理 | 使用 yt-dlp、FFmpeg、Whisper、MediaPipe 和 OpenCV。 |
| 手动剪辑 | 调整片段时间、标题、字幕、画幅、构图和取景位置。 |

## 工作方式

    导入视频
      → 获取或生成字幕
      → 选择内容切片 / 精彩高光 / 推广成片
      → 分析候选片段
      → 编辑草稿
      → 导出 MP4

内容切片以字幕语义为依据。精彩高光可选择字幕分析或视觉分析。推广成片复用候选片段，再生成标题和开场文案。

选择 9:16 时，GoClip 对视频帧进行本地人脸检测和跟踪，生成动态裁切路径。视觉模型负责判断哪些画面是高光，不负责逐帧裁切坐标。

| 制作方式 | 主要输入 | 模型与本地组件 | 默认结果 |
| --- | --- | --- | --- |
| 内容切片 | 字幕、主题、时长 | 文本模型；缺字幕时用本地 Whisper | 语义完整的短片草稿并自动导出 |
| 精彩高光 | 字幕，或授权的抽样画面 | 文本模型，或视觉模型 | 高光候选草稿 |
| 推广成片 | 内容/高光候选 | 文本模型生成标题、Hook | 带发布文案的草稿 |

三种方式共用同一套项目、任务、草稿和导出记录；区别只在候选片段如何产生，以及是否需要云端模型。

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

### 4. 制作第一条短片

1. 上传视频，或粘贴 B 站 / YouTube 链接。
2. 等待导入任务完成。
3. 在制作方案中选择目标。
4. 选择分析模式、目标时长和画幅。
5. 确认模型费用。视觉分析还需要授权上传抽样图片。
6. 点击确认并开始制作。
7. 打开草稿，检查片段、标题、画幅和字幕。
8. 保存草稿并导出。

内容切片自动导出。精彩高光和推广成片默认保留为草稿；制作方案可以启用一键出片。

## 选择哪种制作方式

### 内容切片

适合课程、访谈、演讲、知识讲解和经验分享。

系统从字幕中提取主题，定位时间范围，评分后选择语义完整的片段。没有字幕时，Worker 会在确认制作后运行本地 Whisper。

内容切片使用文本模型，不发送视频帧。

### 精彩高光

适合动作、结果、反应、冲突、关键观点和精彩瞬间。

文本模式从字幕中找高光。视觉模式先抽取视频帧，再让视觉模型识别事件。视觉模式适合游戏、运动、演示和画面驱动的视频。

视觉模式只发送本次任务授权的抽样图片。讲课和口播类视频通常使用文本模式效果更稳定。

### 推广成片

系统先取得内容或高光候选，再调用文本模型生成标题和 Hook。你可以在草稿中修改文案、字幕、画幅和片段边界。

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
   ├─ internal/ai：文本和视觉模型适配、候选评分、标题与 Hook
   └─ Python engine：MediaPipe/OpenCV 人脸跟踪、构图和动态导出
```

一次制作任务按以下阶段执行：

1. Web API 创建项目、导入任务和制作方案；任务状态通过 SSE 推送到页面。
2. Worker 用 yt-dlp、FFmpeg 和 ffprobe 下载并校验素材，导入阶段不会无条件调用模型。
3. 确认制作后，内容目标走字幕语义分析；高光和推广按选择走字幕或视觉分析。
4. 文本路径生成大纲、时间线、片段评分和标题；视觉路径先扫描帧，再做事件识别和密集复核。
5. 成功的候选写入项目工作区并生成草稿；推广目标在此基础上补充标题、Hook 和发布文案。
6. 选择 9:16 时，Python engine 调用 MediaPipe/OpenCV 跟踪人脸，生成动态 crop path；检测不到人脸时回退居中裁切。
7. 导出任务读取草稿 revision 的不可变快照，用 Python engine 和 FFmpeg 生成并校验 MP4。
8. Worker 保存导出记录和检查点；失败重试会复用已完成阶段，不重复已发布的结果。

视觉路径会保存候选、选择理由和检查点。重试时可以复用已完成阶段。

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
