## Why

数据库误操作（DELETE 忘带 WHERE、UPDATE 跑错、DROP 表）是高频且代价惨痛的事故。现有解决方案——备份恢复需要半小时到半天，binlog/WAL 手动解析过于繁琐——在紧急时刻过于复杂。创始人团队亲身经历："运维在处理客户问题时将一张表的数据全部删除，抢救了半天"。目标是用一个工具将数据恢复从「4 小时 + 找 DBA」压缩到「5 步操作、5 分钟内完成」。

## What Changes

- **创建 MySQL PITR 闪回系统**：Go 原生 Agent + Web 控制台的完整数据恢复平台
- **Go 原生 binlog 解析器**：解析 MySQL 5.7/8.0 ROW 格式 binlog，生成逆向 SQL（INSERT→DELETE, DELETE→INSERT, UPDATE→原值）
- **分批事务回滚引擎**：支持 checkpoint 断点续传和 FK 依赖排序的大数据量回滚
- **反向 WebSocket 隧道**：Agent 通过 mTLS 加密的 WebSocket 连接云平台，无需公网 IP
- **Web 控制台**：React 前端 + REST API，提供 5 步 PITR 向导（连接 DB → 选表 → 选时间点 → 预览 → 执行）
- **Agent 离线模式**：支持本地 CLI 模式（`agent flashback`），无需网络连接
- **加密凭据存储**：AES-256-GCM 加密配置文件，支持 HashiCorp Vault 集成
- **同时支持自建 MySQL 和 RDS/Aurora**：RDS 场景 Agent 部署在同 VPC 的 EC2 上

## Capabilities

### New Capabilities
- `agent-parser`: MySQL 5.7/8.0 binlog ROW 格式解析，生成逆向 SQL。处理 table_map、行事件、DDL 跳过、常用数据类型
- `agent-rollback`: 分批事务执行引擎 + checkpoint 管理器 + FK 依赖排序 + 预检检查
- `agent-communication`: 反向 WebSocket 隧道（Go 标准库），mTLS 双向证书验证，心跳保活，指数退避重连
- `agent-offline`: Agent 本地 CLI 闪回模式（`agent flashback --mysql-dsn=... --time=...`），无需网络连接
- `web-console`: Web 控制台（后端 REST API + React 前端），包括用户/组织管理、Agent 注册、5 步 PITR 向导、操作审计日志

### Modified Capabilities
无——该项目为新项目，无现有 spec

## Impact

- **新项目**：全新 Go + React 代码库，无现有系统影响
- **依赖**：MySQL（5.7/8.0），RDS/Aurora，Go MySQL driver，React，Docker
- **开源**：代码托管在 GitHub，协议待定（MIT vs AGPL）
- **底层工具参考**：mysql-binlog-connector-go（解析参考）、my2sql（验证参考）
