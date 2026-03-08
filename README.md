#  CodeFlicker2API

## 快速开始

### 本地运行

```bash
# 编译运行
go build -o codeflicke2api.exe .
./codeflicke2api.exe

# 或直接运行
go run .
```

### Docker 部署

```bash
# Docker Compose
docker compose up -d

# 手动 Docker
docker build -t codeflicke2api .
docker run -d -p 8080:8080 -v ./data:/app/data codeflicke2api
```

启动后访问 `http://localhost:8080`，使用默认 Token `123456` 登录管理面板。

## 环境变量

| 变量                   | 默认值                       | 说明                 |
| ---------------------- | ---------------------------- | -------------------- |
| `PORT`                 | `8080`                       | 监听端口             |
| `ADMIN_TOKEN`          | `123456`                     | 管理面板登录 Token   |
| `DEFAULT_API_KEY`      | `sk-123456`                  | 默认 API Key         |
| `CODEFLICKER_BASE_URL` | `https://www.codeflicker.ai` | CodeFlicker 上游地址 |
| `DB_PATH`              | `codeflicke2api.db`          | SQLite 数据库路径    |


## 项目结构

```
codeflicke2api/
├── main.go              # 入口，路由注册
├── config.go            # 配置加载
├── database.go          # 数据库模型
├── account.go           # 账号轮询池
├── middleware.go         # 鉴权中间件
├── upstream.go           # 上游请求客户端
├── handler_openai.go     # OpenAI 兼容端点
├── handler_admin.go      # 管理面板 API
├── web/index.html        # 管理面板前端
├── Dockerfile
├── docker-compose.yml
└── README.md
```

## 技术栈

Go + Gin + GORM + SQLite
