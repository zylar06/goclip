# AutoClip Go · Web 服务端

独立的 Go 迁移版本，复用 AutoClip 的 React 剪辑器。没有桌面端、
Python 业务服务、Redis 或 Celery。原项目与桌面端数据无需改动。

**验收状态：**本地 Go/前端测试、真实转写和 MP4 导出已通过；
Linux 二进制与 Compose 配置已验证，**完整 Linux Docker 部署仍待实机验收**。
详见 `doc_auto/verification.md`，不把模拟模型测试当作真实云模型验收。

## Docker 一键启动

要求：Linux **amd64**，Docker Engine + Compose v2，建议 4 核 / 8 GiB，
足够容纳原片和渲染临时文件的本地磁盘（不要用 NFS）。

```sh
docker compose up -d --build
```

Windows 上先安装 Docker Desktop，使用 WSL2 / Linux 容器后端。启用系统组件后
如提示重启，先重启电脑，再打开 Docker Desktop 并确认首次使用协议。
`docker info` 能返回服务端信息后再执行上述命令；仅安装 CLI 不代表容器引擎已启动。

浏览器打开 `http://127.0.0.1:8080`。首构建预计 **10–25 分钟**，
需要访问 Debian、Go/npm、GitHub 和 Hugging Face；镜像包含 CPU Whisper base、
FFmpeg、yt-dlp、Deno 和字体。之后处理任务不会现场安装依赖。

```sh
docker compose ps
docker compose logs --tail=100 web worker
```

**没有登录、项目隔离或配额，所有访问者共享视频、设置及模型费用。**
默认只绑定回环地址；若需要局域网访问，复制 `.env.example` 为 `.env`，
将 `BIND_IP` 改为服务器指定 LAN/VPN 地址。不要直接开放公网。
模型 Key 在网页设置里分别保存，数据库加密，不回传明文。

## 使用

1. 设置文本/视觉模型并分别测试。例如阿里云兼容接口使用
   `https://dashscope.aliyuncs.com/compatible-mode/v1`，文本模型和视觉理解
   模型分别填写自己有权限的模型名。不要填原生多模态 generation 地址，
   不要将图片生成模型当作视觉理解模型。
2. 上传视频（可附 SRT），或填写完整 Bilibili / YouTube 视频页面链接。
   默认限制 4 GiB / 2 小时。没有字幕时使用 CPU 转写；没有音轨时可选择视觉分析。
3. 等待导入完成，确认分析方式、目标和制作参数后开始。只有勾选视觉许可，
   才会向所配置的模型发送抽样图片。长视频不是逐帧视觉分析。
4. 查看草稿、修改镜头顺序与时间、合辑、字幕、构图和开头文字。
5. 点击导出，任务状态为 **completed 且 MP4 可下载** 才是成片完成。
   草稿完成不是视频已经渲染完成。

支持独立任务进度、SSE 与刷新恢复、取消、显式重试、草稿修订号以及不可变导出快照。
CPU 首版输出 30 fps；“原画幅”保持比例但最长边不超过 1920，横/竖屏为 1920×1080 / 1080×1920。
模型阶段显示工作阶段，不编造百分比；原生下载和渲染显示真实进度。
取消/崩溃不会删除原片，过期 worker 任务会标为 interrupted，需手动重试。
**超时请求可能已经计费，点击重试前请确认。**

平台登录：容器不能读取浏览器 Cookie。可在设置页上传 Netscape `cookies.txt`；
仅使用自己的授权凭据，不保证平台风控、地区限制、登录限制或全部视频可下载。

## 开发

```sh
go test -timeout 180s ./...
cd web
npm ci
npm run typecheck
npm test
npm run build
cd ..
go run -buildvcs=false ./cmd/autoclip web
# 另一个终端，需安装/指定 ffmpeg、ffprobe、yt-dlp、whisper-cli 和 base 模型
go run -buildvcs=false ./cmd/autoclip worker
```

配置环境变量见 `.env.example`。本地原生工具路径可设置 `FFMPEG_PATH`、
`FFPROBE_PATH`、`YTDLP_PATH`、`WHISPER_PATH`、`WHISPER_MODEL`、`FONT_DIR`。
HTTP 默认 `127.0.0.1:8080`，数据默认 `./data`。不自动导入桌面端 Key 或历史项目。

## 文档与验收

- `doc_auto/architecture.md`：接口与架构边界。
- `doc_auto/operations.md`：故障、备份、恢复及回滚。
- `doc_auto/verification.md`：实际运行的测试、待验收项目与已知限制。
- `api/openapi.json`：OpenAPI 契约；`web/src/generated/api.ts` 为生成类型。
- `tools.lock.json`：镜像工具和模型锁定信息。

MIT 项目源码；容器内 FFmpeg、字体等采用各自许可证，见
`THIRD_PARTY_NOTICES.md`。请只处理自己有权使用的视频。
