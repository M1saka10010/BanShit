# 参与贡献

需要 Go 1.25+；发布构建无需 Node.js、npm 或 CGO。竞态测试需要支持 race 的平台及 C 编译器。

## 开发与验证

复制 config.example.json 为 config.json，设置至少 12 位随机管理密码后运行 `go run .`。真实采集及模型请求可能产生费用；离线演示请运行 `make demo`，使用独立临时数据和随机密码。

提交前运行 `make check`（测试、竞态检测、静态检查和单二进制构建）。也可分别执行 `make test`、`make race`、`make vet`、`make build`。首次构建 gotd 协议代码内存需求较高，可使用 `GOMAXPROCS=2 GOGC=30 go build -p 1 .`。

前端在 public/ 中，保持原生 ES Module、严格 CSP 和零构建方式。修改后须重新编译二进制才能更新嵌入资源。应用及接口测试位于 internal/app/，Telegram 核心测试位于 internal/telegram/，均与实现同包以保留内部可见性；可分别运行 `go test ./internal/app` 和 `go test ./internal/telegram`。应用测试仅从 ../../config.example.json 读取配置模板，运行数据写入 t.TempDir()，不读取真实 config.json 或 data/；未来测试夹具使用各包的 testdata/。

不要提交 config.json、data/、Telegram 会话、配置临时文件、浏览器快照和未脱敏截图。提交 issue 时删除密钥、手机号、Cookie 和验证码。

## 发布前

参见 [发布检查清单](docs/releasing.md)。项目采用 [MIT 许可证](LICENSE)，分发时须保留版权与许可声明。
