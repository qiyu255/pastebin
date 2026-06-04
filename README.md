# Pastebin

一个简单的 Pastebin web 服务端，Go 语言实现，SQLite 存储。

## 特性

- 纯文本 paste 服务，无所有权概念，任何人可读、可写、可删
- 锁定 paste 防止覆盖和过期（需管理员认证）
- 基于 ID 长度自动分配 TTL
- 随机字符串 ID 生成，碰撞检测与长度递增
- 固定时间窗口速率限制，读写分开，基于 IP
- 条件缓存支持（304 Not Modified）
- 数据库大小超限自动拒绝写入
- 过期 paste 定时清理
- 统计报告（数据库大小、数量、内存占用等）
- 日志轮转（按时间或大小）
- 请求唯一标识与耗时统计
- 并发请求数限制
- 浏览器访问返回 Web 界面，命令行访问返回帮助文本

## 快速开始

```bash
# 构建
make

# 运行
./pastebin -config config.json

# 生成管理员密钥哈希
./passwd
```

## 配置

编辑 `config.json` 配置服务参数，详见 DESIGN.md。

## API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/` | 浏览器返回 Web 界面，命令行返回帮助文本 |
| GET | `/?action=stats` | 查看统计信息 |
| GET | `/:id` | 获取 paste 内容 |
| POST | `/` | 创建 paste（自动生成 ID） |
| POST | `/:id` | 创建或覆盖 paste |
| DELETE | `/:id` | 删除 paste |
| PUT | `/:id?action=lock` | 锁定 paste（需管理员） |
| PUT | `/:id?action=unlock` | 解锁 paste（需管理员） |

所有响应均为纯文本格式（浏览器首页除外）。

## 示例

```bash
# 创建 paste
echo "hello world" | curl -s localhost:8080/

# 读取 paste
curl localhost:8080/<id>

# 通过文件上传
curl localhost:8080/ -F file=@hello.txt
```

## 项目结构

```
pastebin/
├── cmd/            # 入口程序
│   ├── pastebin/   # 主服务入口
│   └── passwd/     # 密码哈希工具
├── internal/       # 内部包
│   ├── handler/    # HTTP 处理器与中间件
│   ├── store/      # SQLite 数据库操作
│   ├── model/      # 数据模型
│   ├── auth/       # 管理员认证
│   ├── ratelimit/  # 速率限制
│   ├── idgen/      # ID 生成器
│   ├── stats/      # 统计报告
│   ├── cleanup/    # 过期清理
│   ├── config/     # 配置解析
│   └── logutil/    # 日志轮转
├── config.json     # 配置文件
├── index.html      # Web 界面
├── help.txt        # 命令行帮助文本
├── DESIGN.md       # 设计文档
├── Makefile        # 构建脚本
└── README.md       # 本文件
```

## 技术栈

- Go 1.26 + net/http
- SQLite (github.com/mattn/go-sqlite3)
- 日志: log/slog
- 认证: bcrypt (golang.org/x/crypto)

## 许可

MIT
