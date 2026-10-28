# Docker 部署

需要 Docker Engine 和 Compose v2。以下命令在部署主机的项目根目录执行；本项目不会自动替你部署。

## 初始化持久化目录

首次部署执行（已有配置时不要覆盖）：

    test ! -e deploy/config/config.json && mkdir -p deploy/config deploy/data && cp config.example.json deploy/config/config.json

编辑 deploy/config/config.json：

- 将 host 改为 0.0.0.0，port 保持 3000（容器必须监听所有网卡，宿主端口默认仅绑定 127.0.0.1）。
- 将 adminPassword 改为至少 12 位随机密码，示例密码会被拒绝。
- 填写模型和频道配置，或启动后通过后台填写。

Linux 宿主机设置权限：

	sudo chown -R 10001:10001 deploy/config deploy/data
	sudo chmod 700 deploy/config deploy/data
	sudo chmod 600 deploy/config/config.json

镜像以 UID/GID 10001 非 root 运行。只对上述专用部署目录设置权限，不要对整个源码目录执行递归 chown。

**必须挂载整个配置目录且保持可写，不能只绑定单个 config.json，也不能只读挂载。** 后台保存采用同目录临时文件加 rename，单文件挂载可能导致保存失败。SQLite、WAL 和 Telegram 会话均持久化在 deploy/data；Compose 要求目录提前创建，避免意外生成 root 所有的目录。

## 构建和启动

	docker compose config --quiet
	docker compose up -d --build
	docker compose logs -f --tail=100 banshit

默认访问 http://localhost:3000/admin。需要更换宿主机端口时，复制 `.env.example` 为项目根目录的 `.env`（已有文件则直接编辑，不要覆盖），例如设置：

```dotenv
BANSHIT_PORT=3001
```

Compose 自动读取 `.env`；未设置或值为空时使用 3000。执行 `docker compose up -d` 应用修改后，访问 http://localhost:3001/admin。仅改变宿主机映射端口，绑定地址仍为 127.0.0.1，容器内配置的 port 必须保持 3000。`BANSHIT_PORT` 是 Compose 插值变量，不是应用自身的环境变量；使用 1–65535 内未被占用的端口。`.env` 已被 Git 忽略。

默认从 Docker Hub 获取基础镜像。网络无法访问 Docker Hub 时，可指定镜像源：

	GO_IMAGE=docker.1panel.live/golang:1.26-alpine RUNTIME_IMAGE=docker.1panel.live/alpine:3.23 docker compose build
	docker compose up -d --no-build

构建还需访问 Go 模块源和 Alpine 软件源。首次编译 Telegram 协议占用较多内存，Dockerfile 已限制编译并发；建议准备至少 4 GiB 可用内存。基础镜像采用版本标签；严格可复现发布应进一步固定经过验证的镜像 digest。

## 网络与代理

容器不会自动继承宿主代理。需要代理时，在 Compose 的 environment 中显式增加 ALL_PROXY、HTTPS_PROXY、HTTP_PROXY 和按需设置的 NO_PROXY。Telegram 代理优先级及支持协议见 README。

容器里的 127.0.0.1 指向容器自己，不能用它访问宿主机代理。使用容器可达的代理地址；Linux Docker Engine 可按需添加 extra_hosts: ["host.docker.internal:host-gateway"] 并使用该域名，宿主代理还须监听对应可达接口并由防火墙限制访问。代理凭据属于敏感信息，不要提交带密钥的 Compose 覆盖文件；docker compose config 会展开环境变量，分享输出前须脱敏。

公网访问应在前面加 HTTPS 反向代理，设置 secureCookie=true，trustedProxies 填写应用实际看到的可信代理 IP 或最小 CIDR，不能盲目填写宿主机回环地址或 /0。若反代也在容器中，应通过共享网络访问 banshit:3000，而不是反代容器自己的 localhost。

## 升级和备份

Compose 将 `/tmp` 挂载为上限 256 MiB 的 tmpfs，为 SQLite 查询、排序等操作提供临时空间。容量按实际使用消耗内存，不会启动就全部占用。旧配置的 16 MiB 上限可能导致 `database or disk is full (13)`，即使数据盘仍有空间。升级此设置后执行 `docker compose up -d --force-recreate banshit`，无需重建镜像；已有配置与数据库挂载保留。不要通过删除 WAL 解决空间错误。

升级前先 docker compose stop，安全备份 deploy/config 和整个 deploy/data，再构建新镜像并启动。切勿复制仍在写入的单个 SQLite 文件当作完整备份。Telegram 会话及归档均是敏感授权凭据。停容器不会删除 bind 挂载数据；不要将 deploy/ 放进源码发布包。

标准输出日志使用 Docker 日志驱动轮转；每次任务会记录开始/结束、耗时、频道统计、排序结果和安全错误摘要，可用 docker compose logs 查看。应用仍没有持久化的逐任务运行日志，日志不会包含 API 密钥、验证码、密码、会话或原始远端响应；采集错误也会显示在后台最近状态。服务重启会使后台登录失效，已保存 Telegram 会话可恢复。
