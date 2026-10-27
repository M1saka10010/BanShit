# 发布检查清单

## 发布边界

当前工作目录包含真实配置、数据库和 Telegram 会话。不要直接打包整个工作目录。`.gitignore` 只影响未跟踪文件，不会清除 Git 历史或阻止手动压缩敏感文件。

发布前逐项确认：

1. 项目采用 [MIT 许可证](../LICENSE)。发布物须包含 LICENSE，并确认第三方依赖的许可与声明保留义务；项目许可证不替代依赖或采集内容的授权。
2. 初始化或检查版本库后，审阅 `git status --short`、`git diff --cached --name-only` 和实际提交内容。不要使用未经审阅的全目录打包。
3. 确认 config.json、data/、会话归档、.banshit-config-*、.playwright-cli/、output/、.mimocode/、本地二进制均未跟踪；检查历史是否曾包含凭据。发现泄露须撤销或轮换，删除当前文件不够。
4. 在干净检出中运行 `make check`。验证 config.example.json 仍为无真实密钥的模板。
5. 用演示模式检查首页、后台、嵌入资源与移动端；真实 Telegram/模型连通性需单独授权验收。
6. 从已审阅的提交/标签导出源码（例如 `git archive`），二进制单独作为 Release 附件，附目标平台和 SHA-256。

构建：`CGO_ENABLED=0 go build -trimpath -o banshit .`。部署只需二进制、私有配置及可写数据目录，无需复制 public/。默认路径相对于工作目录，可用 BANSHIT_CONFIG/BANSHIT_DATA 指向源码之外。升级前停服备份，后台会话在重启后失效。

本清单不代表已创建仓库、推送代码或发布 Release；这些操作需维护者单独执行或授权。
