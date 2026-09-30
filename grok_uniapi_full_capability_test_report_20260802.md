# grok-imagine-video / UniAPI 全能力最低预算测试报告

## 1. 报告信息

- 测试日期：2026-08-02（Asia/Shanghai）
- 被测模型：`grok-imagine-video`
- 对外协议：xAI 风格 REST API
- UniAPI 上游协议：REST
- 测试目标：以 UniAPI 文档为主，验证文档列出的创建、轮询、图生视频、输出上传、分辨率、比例、时长、审核字段等能力，并复核平台的 OSS、原始链接、下载协议和账单链路。
- 安全说明：本报告不记录平台 API Key、UniAPI Key、审计 Key 或签名参数。

## 2. 文档基线

本次以以下 UniAPI Markdown/OpenAPI 文档为基线：

1. 创建视频：<https://api-docs.uniapi.ai/api-414228805.md>
2. 获取视频状态：<https://api-docs.uniapi.ai/api-414229563.md>

文档定义的创建接口：

```text
POST https://api.uniapi.io/xai/v1/videos/generations
Authorization: Bearer <UNIAPI_KEY>
Content-Type: application/json
```

文档定义的查询接口：

```text
GET https://api.uniapi.io/xai/v1/videos/{request_id}
Authorization: Bearer <UNIAPI_KEY>
```

创建字段基线：

| 字段 | 文档定义 |
|---|---|
| `prompt` | 视频提示词 |
| `model` | 仅 `grok-imagine-video` |
| `duration` | 整数 `1-15`，默认 `6` |
| `resolution` | `480p`、`720p`，默认 `480p` |
| `aspect_ratio` | `1:1`、`16:9`、`9:16`、`4:3`、`3:4`、`3:2`、`2:3` |
| `image.url` | 可选首帧图片 URL |
| `output.upload_url` | 可选 HTTP PUT 签名上传地址 |

状态响应基线：生成中返回 `status=pending`；完成后返回 `model` 和 `video.url`、`video.duration`、`video.respect_moderation`。

## 3. 总体结论

### 3.1 当前源码

**通过。** 当前工作区源码已覆盖 UniAPI 文档中的全部能力类别，相关聚焦测试和跨渠道回归测试全部通过。

本次验收中发现并修复了三个兼容问题：

1. UniAPI `duration` 原先只接受 `6-15`，与文档的 `1-15` 不一致。
2. UniAPI 未传 `duration` 时原先继承 xAI gRPC 的 `8` 秒默认值，与文档默认 `6` 秒不一致。
3. `output.upload_url` 是写入目标，不一定支持 GET；原逻辑会把其 HTML 页面误抓取为 OSS `.ehtml` 文件。

修复后，UniAPI 分支接受 `1-15` 秒并默认 `6` 秒；客户端上传目标不会再被当成下载源；视频捕获遇到 HTML/JSON/XML 会拒绝，不会作为视频写入 OSS；`/content` 也不会把非视频正文返回给客户端。

### 3.2 当前国内外线上实例

**核心能力已通过，但不是最终源码版本。** 线上现有镜像已通过文生视频、图生视频、480p、720p、1:1、状态轮询、审核字段、正常 OSS、原始链接和 Range 下载测试。

不过，国内外线上实例尚未部署本报告中的最后一轮补丁。因此在部署前：

- 线上 `duration=1` 仍可能被旧版本本地校验拒绝。
- 线上旧的 `output.upload_url` 任务仍可能显示历史 `.ehtml` 伪媒体记录。

结论必须区分为：**源码验收通过，线上待部署后复验。**

## 4. 最低预算测试设计

为避免为每个枚举值分别生成视频，本次采用三层测试：

1. 一条现有 6 秒 480p 文生视频任务，验证基础生成、轮询、正常 OSS、原始链接、下载和 480p 输出。
2. 一条 6 秒 720p 组合任务，同时验证 `image.url`、`output.upload_url`、`1:1`、720p、审核字段和上传结果。
3. 一条新增 1 秒 480p 最低成本任务，同时验证修复后的最小时长和 `output.upload_url` 防误抓逻辑。

其他合法枚举、非法枚举、缺字段和鉴权场景使用自动化测试或不触发上游的 HTTP 校验完成。

## 5. 真实任务证据

### 5.1 文生视频、480p、正常 OSS

- 任务 ID：`task_phSxpUqBFD9hnN5zgyp4QTvWkemUPvZf`
- 创建时间：2026-08-02 21:05:56 +08:00
- 完成时间：2026-08-02 21:06:31 +08:00
- 状态：`completed`
- 模型：`grok-imagine-video`
- 时长：6 秒
- 分辨率参数：480p
- 审核：`respect_moderation=true`
- OSS：正常返回 `.mp4`
- 原始链接：正常返回 `original_url`，域名为 `vidgen.x.ai`
- OSS 文件：213,110 字节
- SHA-256：`2E5B0C39C2080705E583B348FFB61CFD7F8467B07A9033D79C272F3E917ED4F5`
- 媒体：H.264/AVC + AAC，848x480，约 6.04 秒

该任务证明：基础文生视频、轮询完成、480p 视频、审核字段、上游原始地址、平台 OSS 地址和可播放 MP4 均生效。

### 5.2 图生视频、720p、1:1、上传目标组合任务

- 任务 ID：`task_EYAo4MtSv3YTUFlTKONCFPq7HRgUIZyz`
- 创建时间：2026-08-02 21:32:37 +08:00
- 完成时间：2026-08-02 21:33:22 +08:00
- 状态：`completed`
- 参数：`duration=6`、`resolution=720p`、`aspect_ratio=1:1`
- 输入：使用 `image.url` 提供首帧图片
- 输出：使用 `output.upload_url` 提供 PUT 目标
- 审核：`respect_moderation=true`
- 上游 PUT：`Content-Type: video/mp4`
- 上游 PUT 文件：930,711 字节
- 下载文件 SHA-256：`7F5F1FA51329CA12FCB5307AAA9198E2BEFA2762B4B01CA1064D21F86CECC3DC`
- 媒体：H.264/AVC + AAC，720x720，约 6.04 秒

已提取首帧、中间帧和末帧进行视觉检查：首帧保留输入图片主体，后续帧产生旋转运动，证明 `image.url` 不只是被请求接受，而是实际参与生成。

证据文件：

| 文件 | 用途 | SHA-256 |
|---|---|---|
| `.tmp-local-run/uniapi-live-i2v-source.png` | 输入首帧 | `5623758411AE04332355F0B5711912E1B229F581DC90C6C46E3463E064A444CD` |
| `.tmp-local-run/uniapi-live-i2v-frame-start.png` | 输出起始帧 | `8C830DE636F843C8B14327D68CCD7869FF60744A16695D9A5DBDBED176BA3E59` |
| `.tmp-local-run/uniapi-live-i2v-frame-mid.png` | 输出中间帧 | `4903B055B4E22C0494F9F1EEFD9F593EEAFD54B3B2F80B0FDCE7B4B1A481ABE0` |
| `.tmp-local-run/uniapi-live-i2v-frame-end.png` | 输出末帧 | `E4448DB1218BDB228BFF9D4699016B9CB223E0CD920F9586C7F4D0D71968DE22` |

旧线上镜像随后对 PUT 目标发起 GET，获得 156 字节 `text/html`，并误存为 `.ehtml`。这不是 UniAPI 生成失败，而是平台把写入目标误当成下载地址。当前源码已修复该问题。

### 5.3 1 秒最低成本修复验证

- 任务 ID：`task_Ffr6leE3kdLJkGOYQp7ro9Nk2P0Ar6EH`
- 环境：隔离本地实例，复制测试 SQLite 库，使用当前源码
- 创建时间：2026-08-02 22:04:30 +08:00
- 完成时间：2026-08-02 22:04:57 +08:00
- 参数：`duration=1`、`resolution=480p`、`aspect_ratio=1:1`
- 状态链：`in_progress -> in_progress -> in_progress -> completed`
- 审核：`respect_moderation=true`
- 上游 PUT：`Content-Type: video/mp4`
- 上游 PUT 文件：42,458 字节
- 平台账单：25,000 quota，金额数值 `0.05`

该任务证明 UniAPI 上游实际接受 1 秒生成，当前源码也不再在本地阻断。上传目标的 GET 响应为 HTML 时，`/content` 返回 `502` JSON 错误，没有再把 HTML 当视频返回。

本任务后发现同步重写路径仍会创建一条失败的 `response_url` 捕获记录。随后已增加第二层提前排除，确保同一 `output.upload_url` 不进入同步重写；该最后一步已由自动化测试验证，为避免重复收费未再次生成视频。

## 6. 文档能力逐项验收

| 能力 | 验证方式 | 结果 | 说明 |
|---|---|---|---|
| `POST /xai/v1/videos/generations` | 真实上游 | 通过 | 三条任务均成功创建 |
| Bearer 鉴权 | 无鉴权负测 | 通过 | 无 Key 返回 401 |
| `prompt` | 真实任务 + 缺字段负测 | 通过 | 缺失返回 400，不触发上游 |
| `model` | 真实任务 + 未知模型负测 | 通过 | 正确模型透传；未知模型返回 503 |
| `duration=1` | 真实上游最低成本任务 | 通过 | 当前源码接受并完成 |
| `duration=15` | 请求构造和边界测试 | 通过 | 完整透传 15 |
| `duration=16` | HTTP/单元负测 | 通过 | 返回 400 |
| 默认 `duration=6` | 请求构造测试 | 通过 | 已从错误的 8 修正为 6 |
| `resolution=480p` | 真实媒体检查 | 通过 | 输出 848x480 |
| `resolution=720p` | 真实媒体检查 | 通过 | 1:1 输出 720x720 |
| 非法 `resolution=1080p` | 负测 | 通过 | 返回 400 |
| `aspect_ratio=1:1` | 真实媒体检查 | 通过 | 输出 720x720 |
| 其余六种合法比例 | 枚举和请求校验测试 | 通过 | 全部接受并透传 |
| 非法 `aspect_ratio=21:9` | 负测 | 通过 | 返回 400 |
| `image.url` | 真实 720p 图生视频 | 通过 | 首帧及后续运动可视验证 |
| `output.upload_url` | webhook PUT 记录 | 通过 | 两次收到 `video/mp4` |
| `GET /xai/v1/videos/{id}` | 真实轮询 | 通过 | pending 映射为对外 in_progress，完成后为 completed |
| `video.url` | 真实完成响应 | 通过 | 正常下载任务返回上游 URL；上传目标任务返回 PUT 目标 |
| `video.duration` | 真实完成响应 | 通过 | 1 秒、6 秒均正确保存和返回 |
| `video.respect_moderation` | 真实完成响应 + 解析测试 | 通过 | 正常为 true；false 映射为审核失败 |

## 7. 平台扩展能力验收

### 7.1 原始链接与 OSS 链接

普通任务同时返回：

- `metadata.url`：平台 OSS MP4 地址
- `original_url`：UniAPI/xAI 原始视频地址

两者没有互相覆盖，满足“返回 OSS 链接同时保留原始链接”的要求。

`output.upload_url` 是客户端提供的写入目标，不应被伪装成平台 OSS。当前源码保留其为 `original_url`/结果目标，并禁止把 HTML GET 页面当视频。

### 7.2 `/content` 下载协议

对普通 OSS 任务实测：

| 请求 | 结果 |
|---|---|
| `HEAD /v1/videos/{id}/content` | 200，`video/mp4`，Content-Length 正确 |
| 普通 GET | 200，完整 MP4 |
| `Range: bytes=0-15` | 206，返回 16 字节 |
| 超范围 Range | 416，`Content-Range: bytes */213110` |
| 响应头 | 返回 `Accept-Ranges`、`ETag`、`Last-Modified` |

当前 OSS/CDN 对 `If-None-Match` 实测仍返回 200，而不是 304；直接请求 OSS 源站结果相同，因此不是平台代理丢失 ETag。该行为不属于 UniAPI 两份文档的模型能力范围，但缓存客户端不应依赖一定返回 304。

对写入型 `output.upload_url`，GET 返回 HTML 时，当前源码返回 502，不返回伪视频。

## 8. 状态和错误处理

| 场景 | 结果 |
|---|---|
| 无鉴权 | 401 |
| 缺少 `prompt` | 400 |
| `duration=16` | 400 |
| `resolution=1080p` | 400 |
| `aspect_ratio=21:9` | 400 |
| 未知模型 | 503，无可用渠道 |
| UniAPI `status=pending` | 映射为平台 `in_progress` |
| 完成且审核通过 | `completed` + URL + 时长 + 审核字段 |
| 审核不通过且 URL 为空 | 映射为失败，错误码 `moderation_rejected` |
| 视频 URL 返回 HTML/JSON/XML | 502 或媒体捕获失败，不写为视频 |

旧线上版本对 `duration=1` 曾返回 422 `unsupported_upstream_capability`。当前源码修复后，真实 1 秒任务已完成。

## 9. 账单与人民币

### 9.1 6 秒 480p

- quota：150,000
- USD：`0.30000000`
- 汇率：`6.7448`
- RMB：`2.02344`
- 校验：`0.30 * 6.7448 = 2.02344`

### 9.2 6 秒 720p

- quota：210,000
- USD：`0.42000000`
- 汇率：`6.7448`
- RMB：`2.832816`
- 校验：`0.42 * 6.7448 = 2.832816`

### 9.3 1 秒 480p

- quota：25,000
- 金额数值：`0.05`
- 隔离本地库的币种配置为 CNY，因此不能用该本地记录验证线上 USD->CNY 汇率链路。

UniAPI 完成响应不提供实际成本。账单审计明确记录：`upstream actual cost unavailable; keep estimated quota`。因此当前结算来自平台价格表达式，不是上游回传的实际扣费。

当前表达式等价于：

- 480p：`0.05 * seconds`
- 720p：`0.07 * seconds`

图生视频 720p 任务仍按 `6 * 0.07 = 0.42` 计费，没有额外图片输入费。UniAPI 的两份接口文档未定义图片输入价格；是否增加图片费需要以供应商账单或单独价格文档为准。

## 10. 预算记录

| 项目 | 金额 |
|---|---:|
| 既有 6 秒 480p 基础任务 | `$0.30` |
| 本轮 6 秒 720p 组合任务 | `$0.42` |
| 本轮 1 秒 480p 修复任务 | `$0.05` |
| 负测、轮询、下载、单元测试 | `$0.00` |
| 全部证据任务合计 | `$0.77` |
| 本轮新增生成成本 | `$0.47` |

组合测试避免为七种比例、两个分辨率和每个字段分别生成视频。合法枚举由自动化测试完整覆盖，真实视频只覆盖最有辨识度的 480p 横屏和 720p 1:1。

## 11. 源码修复范围

本轮与问题直接相关的修复包括：

1. UniAPI 时长范围改为 `1-15`。
2. UniAPI 默认时长独立设为 `6`，不影响 xAI gRPC 默认值。
3. 保存 `output.upload_url` 到 xAI 任务私有数据。
4. 任务媒体捕获跳过客户端上传目标。
5. 同步响应 URL 重写跳过客户端上传目标。
6. 视频捕获拒绝非 `video/*` 内容，`application/octet-stream` 保留兼容。
7. xAI `/content` 拒绝明确的 HTML/JSON/XML 内容。
8. `/content` 透传 Range/If-Range/条件请求头及必要响应头，并支持 HEAD、206、416。

影响范围限定在 xAI/UniAPI 视频、视频代理和生成媒体捕获。没有修改渠道数据库配置、服务器环境变量、Compose 配置或其他模型价格。

## 12. 自动化回归结果

聚焦测试：

```text
go test ./relay/channel/task/xai
ok github.com/QuantumNous/new-api/relay/channel/task/xai

go test ./service
ok github.com/QuantumNous/new-api/service

go test ./controller
ok github.com/QuantumNous/new-api/controller

go test ./model
ok github.com/QuantumNous/new-api/model
```

跨模块回归：

```text
go test ./relay/... ./controller ./model ./router ./service/...
```

结果：全部通过。覆盖 OpenAI、Gemini、Volcengine、Vidu、Doubao 等已有 relay 包，没有发现其他模型回归。

`git diff --check` 通过，仅出现仓库既有 Windows 行尾提示，没有空白错误。

## 13. 上线前后检查清单

1. 使用当前源码分别构建国内、海外镜像，保留两台服务器各自的 Compose、环境变量、挂载、数据库和渠道配置。
2. 部署后检查 `/api/status=200`、容器运行状态和镜像 ID。
3. 两台服务器分别执行无鉴权 `/v1/videos`，确认路由存在且返回 401。
4. 任选一台执行 `duration=1`、480p 的最低成本任务，确认不再返回 422。
5. 使用一次性 PUT 地址执行 `output.upload_url`，确认收到 `video/mp4`。
6. 查询任务，确认没有 `.ehtml` OSS URL，且后台没有为同一上传目标创建 `response_url` 捕获行。
7. 执行普通不带 `output.upload_url` 的任务，确认 OSS、`original_url`、HEAD、206 和 416 正常。
8. 查询账单，确认 quota、USD、汇率和 RMB 链路与价格表达式一致。

## 14. 最终判定

- UniAPI 文档能力覆盖：**通过**。
- 当前源码功能验收：**通过**。
- 其他模型回归：**通过**。
- 最低预算目标：**通过**，本轮新增生成成本 `$0.47`。
- 国内外线上最终验收：**待部署本轮源码后各执行一次最低成本冒烟测试**。

在部署完成前，不应把旧线上镜像标记为“所有问题已关闭”；部署并完成第 13 节检查后，才能形成最终线上全通过结论。
