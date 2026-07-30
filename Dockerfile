# ===== Stage 1: 构建前端 =====
FROM docker.1ms.run/node:20-alpine AS frontend

WORKDIR /build/web

# 先复制依赖文件，利用 Docker 缓存层
COPY web/package.json web/package-lock.json ./

# 使用国内 npm 镜像安装依赖
RUN npm ci --registry=https://registry.npmmirror.com

# 复制源码并构建
COPY web/ ./
RUN npm run build

# ===== Stage 2: 构建后端 =====
FROM docker.1ms.run/golang:1.24-alpine AS backend

# 使用国内 Go 模块代理
ENV GOPROXY=https://goproxy.cn,direct

WORKDIR /build

# 先复制依赖文件，利用 Docker 缓存层
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并构建
COPY . .

# 静态链接构建（运行时路径 ./web/dist/ 不影响编译）
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o server ./cmd/server/

# ===== Stage 3: 最小运行时镜像 =====
FROM docker.1ms.run/alpine:3.19

# 时区数据和 CA 证书（HTTPS 请求需要）
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# 从 backend 阶段复制二进制
COPY --from=backend /build/server ./

# 从 frontend 阶段复制前端静态资源
COPY --from=frontend /build/web/dist ./web/dist/

# 创建数据目录（运行时持久化 settings.json）
RUN mkdir -p /app/data

EXPOSE 8080

ENTRYPOINT ["./server"]
