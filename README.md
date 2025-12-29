## 分布式视频存储与解码平台

一个基于 Go 的轻量级分布式视频存储与解码示例工程，目标是作为可扩展的视频处理基座，覆盖上传分片合并、秒传、引用计数、元数据管理与简单的 HLS 播放能力。

核心特点
- 支持分片上传（chunked upload）与合并，配合 Redis 做进度/墓碑管理。
- 秒传检测（基于文件内容哈希），避免重复存储与重复上传。
- 数据模型：User ↔ Content（语义内容）↔ FileMeta（具体版本/转码产物），支持多版本管理与引用计数。
- 使用 GORM + MySQL 持久化，Redis 用于分布式锁与临时状态。
- 本地存储适配（store 接口），便于替换为对象存储（如 S3/OSS）。
- 提供最小的 Web 服务与示例客户端代码，后续将提供 HLS 播放示例（使用 hls.js）。

目录结构（简述）
- cmd/server: HTTP 服务程序（Gin）
- internal/handler: HTTP handler（路由层）
- internal/logic: 业务编排层（事务、锁、流程）
- internal/db: 数据模型与 repo（GORM）
- internal/redis: Redis 客户端与分布式锁封装
- internal/store: 存储后端接口与本地实现
- cmd/client: 简易上传客户端示例

快速上手
1. 准备依赖：MySQL、Redis
2. 配置 DSN/Redis 地址（在 config 或环境变量中）
3. 本地启动（示例）：
   - 初始化数据库（首次运行会 AutoMigrate）
   - 启动服务：
     go run ./cmd/server
4. 使用示例客户端上传测试视频或通过浏览器访问 HLS 静态目录（示例：r.Static("/video", "./uploads/hls") 配合 hls.js）

设计与扩展方向（已规划）
- 用户认证（JWT / OIDC 中间件）与权限校验
- 转码任务调度与多机集群支持（消息队列 + worker）
- 支持对象存储（S3/OSS）和 CDN 集成
- HLS/DASH 输出与前端播放器示例（hls.js）
- 更完善的迁移脚本、监控与限流策略

贡献指南
- 欢迎提交 issue / PR。代码风格遵循 gofmt、go vet，数据库变更请提供迁移脚本或说明。
- 重要改动（模型/事务/锁）请加单元测试或说明重放场景。

许可证
- MIT（或你希望的许可证，请在此处替换）

联系方式
- 项目维护者：lzzy