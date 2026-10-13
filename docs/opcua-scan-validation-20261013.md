# OPC UA TUI 扫描验证（2026-10-13）

基线 main `29b4d50acd8d15966659242bca39d523bc208df7`。新增扫描工作区、变量选择、
分批读值、CSV/JSON 快照、单会话批量订阅与 CSV/JSONL 记录；默认不限深度。

## 已执行

- Go 1.27.1 Linux amd64；依赖 go.mod/go.sum 未变，`go mod verify` 通过。
- 最终 `go test -json -p 1 -count=1 -timeout 5m ./...`：716 通过、0 失败、3 跳过，计数包含子测试。
- 默认不限深度更新后的完整 engine/tui race 测试通过；`go vet ./...` 与 `git diff --check` 通过。
- 真实回环 OPC UA 服务器：206 变量扫描/分批读、105 变量跨两个 Monitor 批次共享订阅、
  加密证书 pin、取消，以及 namespace 指纹匹配/不匹配行为通过。
- 确定性测试：600 层末端变量被默认扫描发现，回环不重复；显式深度 1/32/256 及节点
  上限产生不完整结果；分页续点释放、未知 namespace、服务端批次上限和部分错误通过。
- 模拟终端：大小屏、变量选择、重复操作、旧回调、停止退出、未返回值不沿用旧数据、
  磁盘错误可见、状态详情和 namespace 指纹保护通过。
- 导出测试：64 位整数、NaN/Inf 标签、数组/Variant/DataValue/ExtensionObject、公式字符串、
  新文件独占创建、容量/行数上限、写入/flush/close 失败通过；不支持的 opaque 值明确报错。
- 最终 Linux amd64、Linux arm64、Windows amd64 以 CGO_ENABLED=0 交叉构建成功；
  verifybinary 验证 Linux 静态 ELF、Windows 仅系统 DLL；Linux amd64 配置烟测通过。

## 保留的原始失败及复查

初次全 internal race 与三平台交叉编译并行时，既有
`TestUABackgroundSubscriptionDoesNotBlockReadsAndQuits` 在期限内仍处于四窗读取中，
670 通过、1 失败、3 跳过。未修改该测试的超时或断言。
停止编译任务后，同一用例独立连续 3 次通过，随后 `go test -race -p 1 ./internal/...`
为 671 通过、0 失败、3 跳过（这是默认深度更新前的全 internal 计数）。
默认深度更新后完整 engine/tui race 再次通过。后续通过不能单独证明原超时根因。

## 未执行及边界

- 三个跳过项：当前环境无非特权 ICMP socket 权限、可选截图未启用、SQLite 三进程驱动
  互通测试未单独启用。它们不作为通过项。
- 未在工业现场服务器、麒麟 ARM64、Windows 或 ARM64 物理终端上运行验收；交叉编译
  不能代替这些运行验证。未做生产长期采集或断线期间数据无丢失保证。
- 扫描仍有总节点/引用数及请求超时保护，命中时显示不完整。全选只针对已发现变量。
- 新扫描订阅遇断线停止，须重新扫描；不会带着旧 namespace 索引自动重连。
- 不改变服务器数据、不调用方法、不自动信任证书、不增加凭据或权限。
