# Pastebin

一个简单 Pastebin 的 web 服务端

## 技术栈

- 后端：go(1.26) + net/http
- 数据库：sqlite
- 日志: log/slog

## 目录结构

```
pastebin/
├── cmd/            # 入口程序
│   └── pastebin/   # 主服务入口
├── internal/       # 内部包
│   ├── handler/    # HTTP 处理器
│   ├── store/      # 数据库操作
│   ├── model/      # 数据模型
│   ├── auth/       # 管理员认证
│   ├── ratelimit/  # 速率限制
│   ├── idgen/      # ID 生成器
│   ├── stats/      # 统计报告
│   ├── cleanup/    # 过期清理
│   └── config/     # 配置解析
├── config.json     # 配置文件
├── help.txt        # GET / 返回的帮助文本
├── DESIGN.md       # 设计文档
└── CLAUDE.md       # 本文件
```

## 编码约定

- 标准库优先，尽量不引入第三方依赖
- 错误处理使用 `if err != nil` 惯用模式，错误向上传递时用 `fmt.Errorf` 包装上下文
- 日志使用 `log/slog`，http请求对秘密信息脱敏，body 只记录大小
- 时间戳统一使用毫秒级 Unix 时间戳（`time.Now().UnixMilli()`）
- Content-Type 始终为 `text/plain`
- paste 内容以原始字节存储和传输，不做文本解码

## 核心设计

详见 DESIGN.md。几个关键点：

- paste 无所有权概念，任何人可读、可写、可删，除非被锁定
- 锁定后的 paste 不会被覆盖、不会过期（实现：将 expired_at 设为字段最大值）
- 解锁时基于当前时间重新设置过期时间
- ID 生成：随机字符串，碰撞检测耗尽则长度 +1 重试，超过长度限制则拒绝写入
- TTL 基于 ID 长度分配，由配置文件定义
- 速率限制：固定时间窗口，基于 IP，读写分开限制
- 数据库大小超限时拒绝写入，用定时器检查
- 支持条件缓存：使用更新时间等元数据返回 304，不计算内容哈希
- 定时器配置格式：`"1m+20s"` 表示定时 1 分钟，随机偏移 1-20 秒

## 构建与运行

```bash
go build ./cmd/pastebin
./pastebin -config config.json
```

## 辅助工具

- 生成管理员密钥哈希：`go run ./cmd/passwd` 从标准输入读取
