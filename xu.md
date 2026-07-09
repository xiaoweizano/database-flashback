# MySQL Binlog Flashback System（可独立部署）

## 1. 项目目标

设计并实现一个基于 MySQL Binlog 的数据闪回系统，支持：

- 按时间范围回溯数据库变更
- 生成可执行的回滚 SQL
- 提供 Web 管理控制台
- 支持 Docker 一键部署
- 支持低延迟、高吞吐的 binlog 解析能力

系统面向：
- 数据误操作恢复
- 数据审计与回放
- CDC（Change Data Capture）分析

---

## 2. 核心功能模块

---

## 2.1 数据源接入模块

### 功能说明

支持接入 MySQL 数据库实例，并订阅 binlog 数据。

### 要求

- 支持配置 MySQL 连接信息：
  - host
  - port
  - username
  - password
- MySQL 必须开启：
  - binlog_format = ROW
  - binlog_row_image = FULL（推荐）
- 使用只读权限账号（REPLICATION SLAVE / REPLICATION CLIENT）
- 支持 GTID 模式（优先）

---

## 2.2 Binlog 解析模块

### 功能说明

实时解析 MySQL binlog 事件。

### 支持事件类型

- INSERT
- UPDATE
- DELETE

### 输出结构

统一抽象为：

```json
{
  "database": "db_name",
  "table": "table_name",
  "type": "INSERT | UPDATE | DELETE",
  "timestamp": 1710000000,
  "before": {},
  "after": {},
  "primaryKey": "id"
}