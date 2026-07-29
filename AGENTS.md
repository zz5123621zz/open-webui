# 本项目 Codex 执行规范

- 禁止在这台约 1 GiB VPS 的本地 shell、容器或 self-hosted runner 中运行
  任何 `go test` 命令，包括单包、全量、`-race`、`-run`、缓存命中或通过
  Makefile/脚本间接调用的形式。
- GitHub-hosted runner 是唯一例外：仓库 CI 可以运行 `go test`、`go vet`、
  `go test -race`、Go 构建和 Playwright，因为这些任务不消耗本机资源。
  不得把这台 VPS 注册为承载这些任务的 GitHub Actions runner。
- 本机总内存只有约 1 GiB。启动任何可能占用较多内存的编译器、浏览器、
  测试运行器、容器或并行任务前，必须先检查 `free -h`、swap 和当前主要
  进程/容器占用，并评估峰值。
- 无法确认有安全余量时不得启动；运行中若可用内存或 swap 余量变得不安全，
  应停止该任务并如实记录。不得以消耗完 swap、触发 OOM 或影响现有服务为
  代价完成验收。
- 在这台 VPS 上不得启动 Go 测试编译或浏览器/E2E；必须把它们交给上述
  GitHub-hosted CI。其他本地重任务只有在内存前置检查通过时才能运行。
- 除非用户以后明确撤销，本限制持续有效。
