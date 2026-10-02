# 可重复的终端视觉快照

这是开发验收工具，不是客户端运行依赖。

1. 设置 IOTOOLS_SCREENSHOT_DIR 为输出目录，运行 go test ./internal/tui -run TestVisualCaptures
2. 在开发机安装 Pillow 和 Noto Sans CJK 字体
3. 运行 python scripts/visual/render.py 输出目录

图像来自真实 tview 绘制到 tcell SimulationScreen 的单元格。渲染器正确使用双宽字符的主单元格样式，跳过续格的陈旧内容。它们是确定性模拟终端快照，不能代替 Windows Terminal、SSH、物理串口或真实设备验证。

覆盖首页、中文帮助、Modbus矩阵、OPC UA属性、HTTP查询，以及80×24窄终端/请求表单/YAML编辑器。
