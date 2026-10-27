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

访问 http://localhost:3000/admin。若宿主机已有本地实例占用 3000，可将 Compose 的端口改为 127.0.0.1:3001:3000，容器内配置仍用 3000。

默认从 Docker Hub 获取基础镜像。网络无法访问 Docker Hub 时，可指定镜像源：

	GO_IMAGE=docker.1panel.live/golang:1.26-alpine RUNTIME_IMAGE=docker.1panel.live/alpine:3.23 docker compose build
	docker compose up -d --no-build

构建还需访问 Go 模块源和 Alpine 软件源。首次编译 Telegram 协议占用较多内存，Dockerfile 已限制编译并发；建议准备至少 4 GiB 可用内存。基础镜像采用版本标签；严格可复现发布应进一步固定经过验证的镜像 digest。

## 网络与代理

容器不会自动继承宿主代理。需要代理时，在 Compose 的 environment 中显式增加 ALL_PROXY、HTTPS_PROXY、HTTP_PROXY 和按需设置的 NO_PROXY。Telegram 代理优先级及支持协议见 README。

容器里的 127.0.0.1 指向容器自己，不能用它访问宿主机代理。使用容器可达的代理地址；Linux Docker Engine 可按需添加 extra_hosts: ["host.docker.internal:host-gateway"] 并使用该域名，宿主代理还须监听对应可达接口并由防火墙限制访问。代理凭据属于敏感信息，不要提交带密钥的 Compose 覆盖文件；docker compose config 会展开环境变量，分享输出前须脱敏。

公网访问应在前面加 HTTPS 反向代理，设置 secureCookie=true，trustedProxies 填写应用实际看到的可信代理 IP 或最小 CIDR，不能盲目填写宿主机回环地址或 /0。若反代也在容器中，应通过共享网络访问 banshit:3000，而不是反代容器自己的 localhost。

## 升级和备份

升级前先 docker compose stop，安全备份 deploy/config 和整个 deploy/data，再构建新镜像并启动。切勿复制仍在写入的单个 SQLite 文件当作完整备份。Telegram 会话及归档均是敏感授权凭据。停容器不会删除 bind 挂载数据；不要将 deploy/ 放进源码发布包。

标准输出日志使用 Docker 日志驱动轮转，但应用目前没有持久化的逐任务运行日志；采集错误仍主要显示在后台最近状态。服务重启会使后台登录失效，已保存 Telegram 会话可恢复。
