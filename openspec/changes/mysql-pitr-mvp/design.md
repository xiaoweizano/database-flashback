## Context

基于 gstack 生成的《通用数据闪回系统》设计文档和技术评审。项目目标是解决 MySQL 数据库误操作（DELETE 忘带 WHERE、UPDATE 跑错）的快速恢复问题。

当前用户面临的选择：
1. **备份恢复**—太重，需要找备份→重建环境→恢复数据，丢失大量数据
2. **binlog 手动解析**— my2sql/MyFlash 等 CLI 工具，对非 DBA 不可操作
3. **找厂商/DBA**— 等人响应，客户早已发现问题

MVP 目标：Agent + Web 控制台，5 步操作、5 分钟内完成一次 MySQL PITR 闪回。

## Goals / Non-Goals

**Goals:**
- Go 原生 MySQL binlog ROW 格式解析器（5.7/8.0 DML 子集）
- Agent 可部署在自建 MySQL 服务器（Docker/systemd）以及同 VPC EC2（RDS 场景）
- Web 控制台提供 5 步 PITR 向导：连接 DB → 选表 → 选时间点 → 预览逆向 SQL → 执行回滚
- 支持分批事务 + checkpoint 断点续传的大数据量回滚
- 反向 WebSocket 隧道（mTLS）实现 Agent 与云平台通信
- Agent 本地离线 CLI 模式（无需网络）
- 加密凭据存储（AES-256-GCM）
- 平台内置 CA 自动签发 Agent 证书

**Non-Goals:**
- PostgreSQL / Oracle / 达梦 / Doris 支持（第二阶段）
- DDL 回滚（DROP TABLE / TRUNCATE）
- 逐条操作勾选回滚（研究级问题）
- 桌面客户端（Tauri，第二阶段）
- 对象存储归档（增值功能）
- Kubernetes 部署（MVP 只需 Docker Compose）

## Decisions

### 1. binlog 解析：Go 原生实现
**决策**：不包装 my2sql（Python），用 Go 原生实现 MySQL binlog ROW 格式解析。
**理由**：消除 Python 运行时依赖，单二进制部署。MVP 聚焦 MySQL 5.7/8.0 DML 行事件和常用数据类型。
**风险**：时间线被低估——外部评审指出完整解析器需要 3-6 个月（非 2-3 周）。MVP 聚焦子集。
**替代方案考虑**：包装 my2sql（Python）— MVP 更快但引入 Python 依赖。被否决。

### 2. 回滚策略：分批事务 + checkpoint
**决策**：将逆向 SQL 按每批上限分组，每批单独提交事务，完成后写入 checkpoint。
**理由**：单一大事务受 InnoDB 限制（innodb_log_file_size），大数据量场景会失败。
**关键设计**：FK 依赖排序——涉及外键关联表的回滚不满足交换律，checkpoint 管理器需接受依赖排序的批处理计划。

### 3. Agent 通信：标准 WebSocket + 平台内置 CA
**决策**：Agent 通过主动出站的 WebSocket 连接云平台，平台维护连接池，通过隧道推送命令。
**理由**：NAT 穿透最简单（Agent 不需要公网 IP 或暴露端口）。Go 标准库直接支持。
**安全**：mTLS 双向证书验证。平台内置 CA 自动签发 Agent 证书，有效期 90 天，自动轮换。

### 4. MySQL 部署：同时支持自建 + RDS/Aurora
**决策**：自建 MySQL 时 Agent 直接安装在 DB 服务器上。RDS/Aurora 时 Agent 部署在同 VPC 的 EC2。
**理由**：RDS 限制 binlog 访问（无 SUPER 权限、有限保留策略），需要 VPC 内网络读取。设计中需要明确区分两种模式的连接配置和限制。

### 5. 数据库连接器接口
**决策**：从 Day 1 设计 `Connector` 接口抽象，MySQL 实现 MVP。
**理由**：第二阶段扩展 PostgreSQL 等数据库时，只需实现接口即可接入。

```
type Connector interface {
    Connect(cfg ConnConfig) error
    GetBinlogFiles(ctx context.Context) ([]BinlogFile, error)
    ParseBinlog(ctx context.Context, req ParseRequest) (*ParseResult, error)
    ExecuteRollback(ctx context.Context, sqls []string, opts ExecOptions) (*ExecResult, error)
    Preflight(ctx context.Context) (*PreflightResult, error)
}
```

### 6. 离线 CLI 模式
**决策**：Agent 支持本地模式 `agent flashback --mysql-dsn=... --target-table=... --time=... --output=rollback.sql`
**理由**：WebSocket 隧道依赖云平台可达性。网络中断或云平台不可用时，用户仍需本地闪回能力。

### 7. 凭据安全
**决策**：Agent 配置文件使用 AES-256-GCM 加密存储。支持环境变量替换和可选的 HashiCorp Vault。
**数据库凭据**：`REPLICATION SLAVE`, `REPLICATION CLIENT`, `SELECT`。最小权限原则。

## Architecture Diagram

```
┌───────────────────────┐     ┌───────────────────────────┐
│ DB Server / EC2        │     │ Web Platform (Cloud)       │
│ ┌───────────────────┐ │     │ ┌───────────────────────┐ │
│ │ Agent (Go Binary)  │ │     │ │ WebSocket Hub          │ │
│ │                    │ │◄────►│ │ (mTLS tunnel mgmt)    │ │
│ │ • binlog parser    │ │ WS  │ │ • CA certificate mgmt │ │
│ │ • preflight checks │ │     │ │ • heartbeat + reconnect│ │
│ │ • rollback engine  │ │     │ ├───────────────────────┤ │
│ │ • checkpoint mgr   │ │     │ │ REST API               │ │
│ │ • config crypto    │ │     │ │ • auth/org management │ │
│ └───────────────────┘ │     │ │ • agent registration   │ │
└───────────────────────┘     │ │ • PITR workflow state   │ │
                              │ │ • audit log             │ │
                              │ ├───────────────────────┤ │
                              │ │ React Frontend         │ │
                              │ │ • PITR 5-step wizard   │ │
                              │ │ • agent deploy guide   │ │
                              │ │ • operation history    │ │
                              │ └───────────────────────┘ │
                              └───────────────────────────┘
```

## Data Flow (PITR Recovery)

```
User: 选择目标表 + 时间点
  → Web Frontend → POST /api/pitr/start
  → Web Backend → WebSocket → Agent (cmd: "pitr_preflight")
  → Agent: preflight checks (binlog available, format, DDL, disk)
  → Agent → WebSocket → Backend → Frontend: preflight result
  → User: confirm continue
  → Web Backend → WebSocket → Agent (cmd: "pitr_parse")
  → Agent: parse binlog → generate reverse SQL → return preview
  → Agent → WebSocket → Backend → Frontend: preview (affected rows, SQL sample)
  → User: confirm execute
  → Web Backend → WebSocket → Agent (cmd: "pitr_execute")
  → Agent: batch execute with checkpoint
  → Agent → WebSocket → Backend → Frontend: execution result (rows restored, errors)
  → Web Backend: write audit log
```

## Risks / Trade-offs

| Risk | Mitigation |
|------|-----------|
| binlog 解析器开发时间远超预期（3-6 月 vs 2-3 周）| MVP 聚焦 MySQL 5.7/8.0 DML 子集和常用类型，复杂类型（JSON/GEOMETRY）延后 |
| RDS binlog 保留策略不足（min 1 天，max 35 天）| Preflight 中检测最早可用 binlog，超出范围提前告知用户 |
| 回滚中 DB schema 已变化（ALTER TABLE 发生在恢复窗口内）| 解析器跳过 DDL 事件，维护 schema 上下文追踪，向用户警告 |
| 超大回滚（> 100GB binlog）内存压力 | 解析器流式读取 binlog，不加载全部到内存；SQL 文本按批处理 |
| mTLS 证书过期导致 Agent 离线 | Agent 90 天自动轮换，提前 7 天预警 |
| Agent 在 DB 服务器上被攻破 | 最小权限数据库账户，加密凭据存储，审计日志 |

## Open Questions

1. 开源协议：MIT vs AGPL？
2. SaaS 定价：按次付费（~100 元/次）vs 月费订阅？
3. 超大回滚时 checkpoint 恢复语义——用户接受部分恢复还是强制全部回滚？（设计为分批 + 用户确认，但产品层面需明确）
