feedsystem/                 # 仓库根目录
├── backend/                # 后端 Go Module，独立
│   ├── cmd/
│   │   ├── main/
│   │   │   └── main.go     # 程序入口：加载配置、初始化infra、注册路由、启动gin服务
│   │   └── worker/
│   │       └── worker.go   # 工作线程，处理异步任务
│   ├── internal/
│   │   ├── config/         # 配置解析，读取.env / 环境变量
│   │   ├── model/          # GORM模型: user、video
│   │   ├── repo/           # repo层，纯数据库CRUD
│   │   ├── service/        # 业务逻辑层
│   │   ├── handler/        # http处理器
│   │   │   ├── middleware/ # 认证、跨域中间件
│   │   │   ├── user_handler.go
│   │   │   └── video_handler.go
│   │   ├── router/
│   │   │   └── router.go   # 路由注册，区分公开路由 / 需要鉴权路由组
│   │   ├── infra/          # 基础设施初始化
│   │   │   ├── database/   # mysql连接、自动迁移
│   │   │   ├── cache/      # redis
│   │   │   └── mq/         # 二期 rabbitmq（推模式feed）
│   │   └── util/           # jwt工具、统一response返回、工具函数
│   ├── docs/               # swagger api文档
│   ├── configs/            # 配置文件目录
│   │   ├── .env.dev        # 本地开发环境变量
│   │   └── .env.docker     # docker环境变量
│   ├── go.mod
│   ├── go.sum
│   └── README.md           # 后端单独说明：接口、如何启动后端
│
├── frontend/               # 前端 Vite + TypeScript，独立
│   ├── src/
│   │   ├── api/            # axios请求封装，统一后端baseURL
│   │   ├── components/
│   │   ├── views/
│   │   ├── router/
│   │   ├── store/
│   │   └── main.ts
│   ├── package.json
│   ├── vite.config.ts      # 开发环境proxy代理后端8080接口
│   ├── tsconfig.json
│   ├── .env.development
│   ├── .env.production
│   └── README.md           # 前端单独说明
│
├── deploy/                 # 部署相关文件
│   ├── docker‑compose.yml  # 一键拉起 mysql redis rabbitmq + backend + nginx
│   ├── nginx/
│   │   └── nginx.conf      # nginx配置：静态前端 + 反向代理api到go后端
│   └── sql/
│       └── init.sql        # 数据库初始化建表脚本
│
├── .gitignore              # 根gitignore
└── README.md               # 仓库总说明: 项目简介、架构图、开发启动步骤、部署步骤
