# GoClip

在浏览器里把长视频剪成短片。支持本地上传、B 站 / YouTube 链接、AI 选段、手动剪辑和 MP4 导出。

> **只用于本机或可信内网。** 没有登录和用户隔离，访问者共用项目、密钥和模型调用费用，不要直接开放公网。

## 启动

先安装并启动 Docker，使用 Linux 容器；当前镜像面向 amd64。Windows 可用 Docker Desktop。

```sh
git clone https://github.com/zylar06/goclip.git autoclip-go
cd autoclip-go
docker compose up -d --build --wait --wait-timeout 180
```

已有代码就跳过下载，在项目目录运行最后一条命令。首次构建要下载依赖和语音模型，耗时取决于网络。

浏览器打开 `http://127.0.0.1:8080`。打不开时运行 `docker compose ps`，确认 `web` 和 `worker` 都显示 `healthy`。

## 使用

1. **导入视频**：上传文件，可同时附上 SRT 字幕；也可以粘贴 B 站（含 `b23.tv`）或 YouTube 链接。
2. **选择做法**：手动新建草稿，或在导入确认页选择内容切片、高光、推广等制作类型。
3. **确认制作**：AI 制作需要先配置模型，并确认费用及发送内容；导入本身不会自动转写或调用模型。
4. **检查结果**：预览片段，按需调整起止时间、顺序、标题、画幅和字幕。
5. **下载 MP4**：内容切片自动导出；高光、推广默认先生成草稿，手动编辑后需保存并导出，也可在制作前选择一键出片。

手动剪辑不需要云模型。需要字幕但没有可用字幕时，程序可在本地转写。新增字幕默认关闭，也不会移除原视频已有的字幕。当前不支持翻译和 AI 改写。

## 配置模型

在网页的 **设置** 中填写并保存：

| 项目 | 填什么 |
| --- | --- |
| 接口地址（Base URL） | 服务商提供的 Chat Completions 兼容基础地址，不要填完整的 `/chat/completions` 地址 |
| 模型名称 | 服务商提供的模型 ID |
| 密钥（API Key） | 你的密钥；无需鉴权的本地服务可留空 |

按字幕选段配**文本模型**；按画面选段另配**支持图片输入的视觉模型**。先保存再测试，测试和正式分析都可能收费；字幕分析会发送文本，视觉分析会发送你授权的抽样图片。

- 平台要求登录时，在设置中上传自己的 Netscape 格式 `cookies.txt`。
- 改端口等部署配置：将 `.env.example` 复制为 `.env` 后修改，**不要覆盖已有 `.env`**。
- 若已在 `.env` 配置模型，重启服务会重新应用它，覆盖网页中的临时修改。不要提交或分享密钥文件。

## 常用命令

在项目目录执行：

| 操作 | 命令 |
| --- | --- |
| 查看状态 | `docker compose ps` |
| 查看错误日志 | `docker compose logs --tail=100 web worker` |
| 停止，保留数据 | `docker compose stop` |
| 再次启动 | `docker compose start` |
| 更新代码后构建并启动 | `docker compose up -d --build --wait --wait-timeout 180` |
| 修改 `.env` 后应用 | `docker compose up -d --no-build --wait --wait-timeout 180` |

更新前先备份，等正在制作的任务结束再重启。视频、草稿和设置保存在 Docker 数据卷里，**不要用 `docker compose down -v` 排障，会删数据**。备份和恢复步骤见 `doc_auto/operations.md`。

## 目录与文件说明

找页面看 `web/src/pages/`，改剪辑器看 `web/src/features/studio/`；接口在 `internal/httpapi/`，后台任务在 `internal/worker/`，视频处理在 `internal/media/`。

下方列出仓库内的全部受版本管理文件；依赖、构建产物、本地密钥和测试日志不在其中。带 `test` 的文件用于自动测试，不是运行入口。

<details>
<summary>展开完整目录树（每个文件的用途）</summary>

```text
autoclip-go/
├── README.md                         # 项目介绍、启动方法和文件导航
├── LICENSE                           # 项目源码的 MIT 许可证
├── THIRD_PARTY_NOTICES.md             # 第三方工具和资源的许可说明
├── VERSION                           # 应用版本号，前后端共用
├── Dockerfile                        # 安装工具、测试代码并构建运行镜像
├── compose.yaml                      # 启动网页服务和后台任务服务，配置端口与数据卷
├── .env.example                      # 配置模板，不包含真实密钥
├── .dockerignore                     # 不放进 Docker 构建上下文的文件
├── .gitignore                        # 不提交到 Git 的数据、密钥和构建产物
├── .gitattributes                    # Git 文件换行规则
├── go.mod                            # Go 版本要求及后端依赖
├── go.sum                            # Go 依赖的校验记录
├── tools.lock.json                   # 原生工具、模型及基础镜像的版本和校验值
├── .github/
│   └── workflows/
│       └── ci.yml                    # GitHub 自动测试与容器检查
├── api/
│   └── openapi.json                  # 自动生成的后端接口说明
├── cmd/                              # 可执行程序入口
│   ├── autoclip/
│   │   ├── main.go                   # 启动网页、后台任务或健康检查
│   │   ├── model_env.go              # 从环境变量读取并保存模型配置
│   │   ├── main_test.go              # 启动配置和健康检查端口测试
│   │   ├── model_env_test.go         # 模型配置导入、优先级和错误测试
│   │   └── health_ownership_test.go  # 防止备用进程误报后台服务健康
│   └── specgen/
│       ├── main.go                   # 根据 Go 数据结构生成接口说明
│       └── main_test.go              # 接口生成结果测试
├── internal/                         # Go 后端实现
│   ├── domain/                       # 前后端共用的数据定义与规则
│   │   ├── types.go                  # 项目、草稿、任务等数据结构及校验
│   │   ├── types_test.go             # 基础数据校验测试
│   │   ├── production.go             # 制作方案、制作记录和目标结果定义
│   │   └── production_test.go        # 制作选项与数据规则测试
│   ├── httpapi/                      # 浏览器访问的 HTTP 接口
│   │   ├── api.go                    # 项目、设置、草稿、文件及任务接口
│   │   ├── api_test.go               # 请求校验、文件传输和接口安全测试
│   │   ├── production.go             # 制作确认、素材检查、预览与缩略图接口
│   │   └── production_test.go        # 制作接口及重复确认测试
│   ├── store/                        # SQLite 数据保存与任务排队
│   │   ├── store.go                  # 数据库初始化、项目、草稿、任务和设置读写
│   │   ├── store_test.go             # 数据保存、任务恢复和版本冲突测试
│   │   ├── production.go             # 保存制作方案、发布结果及安排导出任务
│   │   ├── production_test.go        # 重复确认、部分成功和结果保存测试
│   │   ├── lease.go                  # 检查任务归属，阻止过期任务写入
│   │   ├── models.go                 # 成组保存加密的模型配置
│   │   ├── models_test.go            # 模型配置保存失败时的回滚测试
│   │   ├── vault.go                  # 密钥文件管理与敏感配置加解密
│   │   ├── legacy_candidates_test.go # 旧项目候选片段的归属兼容测试
│   │   └── review_regression_test.go # 并发领取、过期写入与导入保存测试
│   ├── worker/                       # 领取并执行耗时任务
│   │   ├── worker.go                 # 任务循环、导入、分析、导出和进度更新
│   │   ├── worker_test.go            # 失败、取消与进度反馈测试
│   │   ├── production.go             # 按制作方案转写、分析和准备预览
│   │   ├── production_integration_test.go # 使用真实媒体验证确认到出片的流程
│   │   ├── download_checkpoint.go    # 记录已下载的文件，供导入重试复用
│   │   ├── lock.go                   # 控制后台任务独占数据目录
│   │   ├── lock_unix.go              # Linux 等系统的文件锁实现
│   │   ├── lock_windows.go           # Windows 文件锁实现
│   │   ├── integration_test.go       # 使用真实媒体验证导入与导出
│   │   └── review_regression_test.go # 下载复用、字幕恢复与进程互斥测试
│   ├── media/                        # 下载、转写与视频处理
│   │   ├── media.go                  # 工具路径、处理限制和媒体信息定义
│   │   ├── download.go               # 校验视频链接，调用 yt-dlp 下载
│   │   ├── probe.go                  # 调用 ffprobe 读取时长、尺寸和音轨
│   │   ├── analysis.go               # 调用本地语音转写、抽帧并解析进度
│   │   ├── pcm.go                    # 检查 WAV 音频是否包含有效声音
│   │   ├── sampling.go               # 按指定时间抽帧和生成缩略图
│   │   ├── preview.go                # 生成浏览器能播放的兼容预览
│   │   ├── render.go                 # 拼接片段、处理画幅并导出 MP4
│   │   ├── subtitle.go               # SRT 字幕读写和剪辑后的时间换算
│   │   ├── title.go                  # 加载字体、排版并生成标题图片
│   │   ├── process.go                # 启动外部工具，处理日志、超时和取消
│   │   ├── process_unix.go           # Linux 等系统的子进程管理
│   │   ├── process_windows.go        # Windows 子进程管理
│   │   ├── argv_test.go              # 工具参数、链接安全和导出画幅测试
│   │   ├── download_subtitles_test.go # 平台字幕下载与无字幕导入测试
│   │   ├── fonts_test.go             # 字体文件、许可证和校验值测试
│   │   ├── integration_test.go       # 真实原生工具的媒体处理测试
│   │   ├── operations_test.go        # 探测、下载、转写和导出异常测试
│   │   ├── preview_test.go           # 预览参数、格式校验和失败处理测试
│   │   ├── preview_integration_test.go # 真实预览的音画时间及字幕叠加测试
│   │   ├── process_test.go           # 模拟工具进程，测试日志、超时和取消
│   │   ├── process_linux_test.go     # Linux 子进程组退出与取消测试
│   │   ├── sampling_test.go          # 抽帧时间、顺序和边界测试
│   │   ├── subtitle_test.go          # 字幕解析、换行与时间轴测试
│   │   └── title_test.go             # 标题样式、位置、透明度和字体测试
│   └── ai/                           # 调用模型并整理选段结果
│       ├── client.go                 # 模型接口请求、配置校验和连接测试
│       ├── client_test.go            # 模型接口、图片发送和错误处理测试
│       ├── errors.go                 # 模型错误分类与敏感信息过滤
│       ├── json.go                   # 严格解析模型返回的 JSON
│       ├── text.go                   # 用字幕生成大纲、时间段、评分和标题
│       ├── visual.go                 # 用抽样画面查找和复核片段
│       ├── visual_test.go            # 视觉结果校验与片段边界测试
│       ├── promo.go                  # 根据已有片段生成推广草稿
│       ├── screen.go                 # 根据少量画面建议分析方式和制作类型
│       ├── quality.go                # 检查片段时长、字幕依据和选段质量
│       ├── retry.go                  # 限次重试临时网络或服务错误
│       ├── checkpoint.go             # 保存分阶段结果，避免重试时重复分析
│       ├── pipeline_test.go          # 文本分析全流程和选段规则测试
│       ├── subtask_test.go           # 分块恢复、重试次数和子阶段测试
│       ├── regression_test.go        # 请求安全、缓存与文本分析回归测试
│       ├── refine_replay_test.go     # 视觉复核恢复时避免重复调用的测试
│       ├── integration_feedback_test.go # 推广草稿的不同开头及恢复测试
│       └── prompts/                  # 发给模型的提示词
│           ├── outline.txt           # 提取内容大纲
│           ├── timeline.txt          # 将主题对应到视频时间段
│           ├── scoring.txt           # 给候选片段评分
│           ├── titles.txt            # 为片段生成标题
│           ├── visual.txt            # 从画面中识别事件
│           ├── refine.txt            # 复核片段边界与依据
│           ├── promo.txt             # 生成推广方案
│           ├── screen.txt            # 初步判断适合的制作方式
│           ├── business/
│           │   ├── outline.txt       # 商业类素材的大纲提示词
│           │   └── timeline.txt      # 商业类素材的选段提示词
│           ├── content_review/
│           │   ├── outline.txt       # 内容解读类素材的大纲提示词
│           │   └── timeline.txt      # 内容解读类素材的选段提示词
│           ├── entertainment/
│           │   ├── outline.txt       # 娱乐类素材的大纲提示词
│           │   └── timeline.txt      # 娱乐类素材的选段提示词
│           ├── experience/
│           │   ├── outline.txt       # 经验分享类素材的大纲提示词
│           │   └── timeline.txt      # 经验分享类素材的选段提示词
│           ├── knowledge/
│           │   ├── outline.txt       # 知识类素材的大纲提示词
│           │   └── timeline.txt      # 知识类素材的选段提示词
│           ├── opinion/
│           │   ├── outline.txt       # 观点类素材的大纲提示词
│           │   └── timeline.txt      # 观点类素材的选段提示词
│           └── speech/
│               ├── outline.txt       # 演讲类素材的大纲提示词
│               └── timeline.txt      # 演讲类素材的选段提示词
├── web/                              # React 前端
│   ├── README.md                     # 前端开发、构建和接口生成说明
│   ├── .gitignore                    # 前端依赖和构建产物的忽略规则
│   ├── package.json                  # 前端依赖及开发、测试、构建命令
│   ├── package-lock.json             # 前端依赖的锁定版本
│   ├── index.html                    # 网页入口模板
│   ├── tsconfig.json                 # TypeScript 类型检查配置
│   ├── vite.config.ts                # 开发服务器、接口代理、构建与测试配置
│   ├── public/
│   │   └── favicon.svg               # 浏览器标签页图标
│   ├── scripts/
│   │   └── generate-api.mjs           # 从接口说明生成前端类型
│   ├── src/
│   │   ├── main.tsx                  # 挂载 React 应用并加载样式与语言
│   │   ├── App.tsx                   # 顶部导航、语言切换和页面错误提示
│   │   ├── routes.tsx                # 项目、确认页、设置等页面路由
│   │   ├── index.css                 # 全局颜色、尺寸和明暗主题
│   │   ├── web.css                   # 网页布局、项目列表和小屏适配
│   │   ├── vite-env.d.ts             # Vite 环境变量的类型声明
│   │   ├── api/
│   │   │   ├── client.ts             # HTTP 请求、文件地址和错误处理
│   │   │   ├── contracts.ts          # 界面使用的接口类型与状态定义
│   │   │   └── taskMonitor.ts        # 实时跟踪任务，断线时轮询和重连
│   │   ├── generated/
│   │   │   └── api.ts                # 自动生成的接口类型，不手动修改
│   │   ├── pages/
│   │   │   ├── HomePage.tsx          # 项目列表、搜索和导入窗口
│   │   │   ├── ImportReview.tsx      # 导入后的素材检查与制作确认页
│   │   │   ├── ProjectPage.tsx       # 项目结果、素材、草稿和任务管理
│   │   │   └── SettingsPage.tsx      # 文本模型、视觉模型和 Cookie 设置
│   │   ├── components/
│   │   │   ├── AnalysisPanel.tsx     # 分析模式、目标和费用确认表单
│   │   │   └── TaskPanel.tsx         # 任务进度、取消和失败重试
│   │   ├── features/
│   │   │   └── studio/              # 剪辑工作区
│   │   │       ├── StudioEditor.tsx  # 片段时间、顺序、标题和画幅编辑
│   │   │       ├── CandidatePicker.tsx # 浏览并选择候选片段
│   │   │       ├── DraftPlayer.tsx   # 按编辑顺序连续预览片段
│   │   │       ├── DraftResultCard.tsx # 单个草稿的缩略图、预览和操作
│   │   │       ├── StudioResults.tsx # 展示制作结果与草稿列表
│   │   │       ├── StudioDownloadLink.tsx # 已完成视频的下载入口
│   │   │       ├── PlanSummary.tsx   # 制作方案的编辑、保存、确认与恢复
│   │   │       ├── SourcePreview.tsx # 原视频播放和兼容预览转换
│   │   │       ├── TitleArtwork.tsx  # 显示后端生成的标题预览
│   │   │       ├── titlePresets.ts   # 标题模板、默认参数和编辑校验
│   │   │       ├── draftExportState.ts # 判断草稿修改后是否需要重新导出
│   │   │       ├── api.ts            # 将后端项目数据转成剪辑器数据
│   │   │       ├── types.ts          # 剪辑器的数据类型
│   │   │       ├── useWorkspace.ts   # 加载项目、监听任务并刷新结果
│   │   │       └── studio.css        # 剪辑器布局与样式
│   │   ├── ui/
│   │   │   ├── index.tsx             # 通用按钮、表单和区块组件
│   │   │   └── ac.css                # 通用组件样式
│   │   ├── i18n/                    # 界面语言
│   │   │   ├── index.ts              # 初始化翻译并切换语言
│   │   │   ├── language.ts           # 读取语言偏好及系统语言
│   │   │   ├── web.zh.json           # 网页功能的补充中文文案
│   │   │   ├── production.zh.json    # 制作流程的补充中文文案
│   │   │   └── locales/
│   │   │       ├── zh.json           # 中文
│   │   │       ├── en.json           # 英文
│   │   │       ├── es.json           # 西班牙文
│   │   │       ├── fr.json           # 法文
│   │   │       ├── ja.json           # 日文
│   │   │       ├── ko.json           # 韩文
│   │   │       ├── pt.json           # 葡萄牙文
│   │   │       └── ru.json           # 俄文
│   │   └── assets/
│   │       └── title-presets/        # 标题模板示意图，不是导出成片
│   │           ├── arena.webp       # 竞技风格示意图
│   │           ├── comic.webp       # 漫画风格示意图
│   │           ├── editorial.webp   # 杂志风格示意图
│   │           ├── frosted.webp     # 磨砂风格示意图
│   │           ├── neon.webp        # 霓虹风格示意图
│   │           └── pixel.webp       # 像素风格示意图
│   └── tests/                       # 前端自动测试
│       ├── setup.ts                  # 测试环境初始化
│       ├── fixtures.ts               # 测试用项目、草稿和任务数据
│       ├── api.test.ts               # 接口请求参数与错误处理测试
│       ├── contract.test.ts          # 接口类型与开发服务器配置测试
│       ├── generator.test.ts         # 接口类型生成器测试
│       ├── editing.test.ts           # 片段移动、替换和编辑校验测试
│       ├── editor.test.tsx           # 编辑器保存、复制与导出交互测试
│       ├── player.test.tsx           # 多片段连续播放和切换测试
│       ├── import-readiness.test.tsx # 导入失败恢复与制作前置检查测试
│       ├── plan-recovery.test.tsx    # 刷新、重复确认和费用授权恢复测试
│       ├── production.test.tsx       # 制作方案保存与确认测试
│       ├── routes.test.tsx           # 导入到确认再到项目页的路由测试
│       ├── settings.test.tsx         # 模型保存、测试和 Cookie 管理测试
│       ├── taskMonitor.test.ts       # 任务监听、断线重连和轮询测试
│       ├── theme.test.ts             # 明暗主题的文字可读性测试
│       ├── title-enabled.test.tsx    # 标题开关及旧草稿兼容测试
│       ├── titleVersions.test.ts     # 标题模板版本支持范围测试
│       ├── workbench.test.tsx        # 项目搜索、导入窗口和界面交互测试
│       ├── workflows.test.tsx        # 上传文件、字幕和链接导入测试
│       └── workspace.test.tsx        # 任务变化后刷新草稿与导出结果测试
├── assets/
│   └── fonts/                        # 视频标题和字幕使用的字体
│       ├── README.md                 # 字体来源、转换方式和使用说明
│       ├── manifest.json             # 原始字体及许可证的校验清单
│       ├── static-manifest.json      # 中文静态字体的转换参数和校验清单
│       ├── Anton-Regular.ttf         # Anton 标题字体
│       ├── Anton-OFL.txt             # Anton 字体许可证
│       ├── BarlowCondensed-Black.ttf  # Barlow Condensed 粗体
│       ├── BarlowCondensed-BlackItalic.ttf # Barlow Condensed 粗斜体
│       ├── BarlowCondensed-OFL.txt    # Barlow Condensed 字体许可证
│       ├── NotoSansSC.ttf            # 保留来源的思源黑体可变字体
│       ├── NotoSansSC-StaticBold.ttf  # 实际用于中文渲染的静态粗体
│       ├── NotoSansSC-OFL.txt         # 思源黑体许可证
│       ├── PressStart2P-Regular.ttf  # 像素风格字体
│       └── PressStart2P-OFL.txt       # 像素字体许可证
├── scripts/                          # 构建检查和整套流程测试脚本
│   ├── apt-setup.sh                  # 配置镜像构建使用的 Debian 软件源
│   ├── apt-setup.test.mjs            # 软件源配置与非法输入测试
│   ├── deployment.test.mjs           # Docker 构建及 Compose 配置测试
│   ├── ci.test.mjs                   # 检查 CI 保留必要测试与运行参数
│   ├── smoke-docker.sh               # 构建并检查容器导入、导出和重启流程
│   ├── smoke-docker.test.mjs         # 容器测试脚本自身的回归测试
│   ├── container-smoke.mjs           # 通过容器 API 验证视频处理流程
│   ├── local-smoke.mjs               # 隔离启动本地服务，验证导入导出
│   ├── frontend-browser.mjs          # 只读检查真实浏览器中的页面并截图
│   ├── parity-browser.mjs            # 隔离运行真实浏览器的完整制作流程
│   └── parity-browser.test.mjs       # 浏览器测试脚本自身的回归测试
└── doc_auto/                         # 详细设计、运维与测试记录
    ├── architecture.md               # 模块分工和接口设计
    ├── backend.md                    # 后端数据、任务和接口实现说明
    ├── frontend.md                   # 前端接口、构建和交互说明
    ├── frontend-workbench.md         # 工作区界面设计与验证记录
    ├── ai.md                         # 模型调用、分析步骤和重试规则
    ├── media.md                      # 转写、字幕、抽帧和视频渲染说明
    ├── operations.md                 # 部署、排障、备份和恢复
    └── verification.md               # 已执行的测试及尚未验证的事项
```

</details>

本机还可能有 `.env`（私有配置）、`artifacts/`（测试输出）、`web/node_modules/`（前端依赖）和 `web/dist/`（构建后的网页），这些不提交到仓库。

开发前端见 `web/README.md`；后端设计与验证见 `doc_auto/`。源码采用 MIT，第三方组件许可见 `THIRD_PARTY_NOTICES.md`。
