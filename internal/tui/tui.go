// Package tui provides one keyboard-first interface for every protocol.
package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"go.yaml.in/yaml/v3"
)

const help = `IOTOOLS · 中文操作帮助

Tab / Shift-Tab   在请求列表、详情、结果、搜索框之间切换
Alt + ←/→        调整左栏宽度；Alt + ↑/↓ 调整结果面板高度
↑ ↓ / j k        选择已保存的请求
Enter / F5       执行请求；修改数据前必须确认
F2               在协议专用视图和原始结果之间切换
F3               在表单内直接编辑所选请求和参数
F4               在终端内直接编辑 YAML 请求配置
F6               切换环境配置（profile）
F7               当前 HTTP 响应 jq / SQLite 历史只读查询
F8               取消正在执行的请求或订阅
F9               OPC UA 连接历史（密码不保存）
F10              OPC UA 独立后台订阅面板
F11              HTTP历史列表/查看/明确删除
Ctrl-Y           在结果表复制所选行（明确确认后）
Ctrl-L           清空结果
? / F1           打开本帮助
Ctrl-C / q       取消当前任务并退出

配置编辑器：Ctrl-S 校验并保存，Esc 放弃修改
HTTP：展开响应树；F2 查看完整原始结果
MQTT：v 载荷；h 历史表；g 图表；/ 搜索；o/O 全部展开/折叠；历史 s 选择字段
OPC UA：Enter 浏览，a 属性，f 引用，r 读取，s 订阅，c 方法参数，g 路径，属性表 e 编辑，退格返回
Kafka：选择主题后按 Enter 开始只读消费
Modbus：m 矩阵，+/- 调整列数，p 固定寄存器，l 添加标签，f 仅看固定项，d 建立差值快照，S 保存快照，O 载入对比
Modbus 高级：C 列，K 快捷键，I 导入标注，E 导出标注，D 导出 CSV

打开程序或选择请求不会自动连接服务器
环境变量：${名称}；敏感信息：${env:变量名}
配置文件只保存变量引用，不自动保存明文凭据
界面结果数量有限；需要完整采集时使用命令行 JSON 输出`

type UI struct {
	localCancels            map[uint64]context.CancelFunc
	nextLocalWork           uint64
	uaCopyPending           bool
	uaCopyValue             string
	uaSubscriptions         map[string]*liveUASubscription
	uaSubTable              *tview.Table
	topLayout, mainLayout   *tview.Flex
	listWidth, resultHeight int
	lastHTTPBody            []byte
	HTTPHistoryPath         string
	App                     *tview.Application
	pages                   *tview.Pages
	list                    *tview.List
	detail, result, status  *tview.TextView
	search                  *tview.InputField
	collection              *config.Collection
	raw                     []byte
	path, profile           string
	readonly                bool
	indexes                 []int
	selected                int
	cancel                  context.CancelFunc
	running                 bool
	quitting                bool
	mu                      sync.Mutex
	events                  []string
	inspector               *inspector
	resultPages             *tview.Pages
	visual                  bool
	lastRequest             config.Request
	mqttPreviewPending      map[string]any
	methodArgumentsPending  map[string]any
	navigation              []config.Request
	focus                   int
}

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return '�'
	}, s)
}
func New(path, profile string, readonly bool) (*UI, error) {
	c, b, e := engine.LoadCollection(path)
	if e != nil {
		return nil, e
	}
	if profile == "" {
		profile = c.DefaultProfile
	}
	if profile != "" {
		if _, ok := c.Profiles[profile]; !ok {
			return nil, fmt.Errorf("unknown profile %q", profile)
		}
	}
	u := &UI{App: tview.NewApplication(), collection: c, raw: b, path: path, profile: profile, readonly: readonly}
	u.pages = tview.NewPages()
	u.list = tview.NewList().ShowSecondaryText(true)
	u.list.SetBorder(true).SetTitle(" 请求集 Collections ")
	u.detail = tview.NewTextView().SetWrap(true)
	u.detail.SetBorder(true).SetTitle(" 请求详情 Request ")
	u.result = tview.NewTextView().SetWrap(false)
	u.result.SetBorder(true).SetTitle(" 原始结果 Results / live events ")
	u.inspector = newInspector(u)
	u.resultPages = tview.NewPages().AddPage("raw", u.result, true, false).AddPage("visual", u.inspector.pages, true, true)
	u.visual = true
	u.status = tview.NewTextView().SetTextColor(tcell.ColorAqua)
	u.search = tview.NewInputField().SetLabel(" 搜索: ")
	u.search.SetChangedFunc(func(s string) { u.populate(s) })
	top := tview.NewFlex().AddItem(u.list, 30, 1, true).AddItem(u.detail, 0, 2, false)
	root := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(u.search, 1, 0, false).AddItem(top, 0, 1, true).AddItem(u.resultPages, 0, 1, false).AddItem(u.status, 2, 0, false)
	u.topLayout = top
	u.mainLayout = root
	u.listWidth = 30
	u.pages.AddPage("main", root, true, true)
	u.list.SetChangedFunc(func(index int, main, secondary string, shortcut rune) {
		if index >= 0 && index < len(u.indexes) {
			u.selected = u.indexes[index]
			u.preview()
		}
	})
	u.list.SetSelectedFunc(func(_ int, _, _ string, _ rune) { u.execute() })
	u.populate("")
	if len(c.Requests) > 0 {
		u.inspector.reset(c.Requests[0])
	}
	u.setStatus("就绪 · Enter 执行 · F4 编辑 · F6 环境 · F8 取消 · ? 帮助")
	u.App.SetRoot(u.pages, true).EnableMouse(true).EnablePaste(true)
	u.App.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyCtrlC {
			u.quit()
			return nil
		}
		name, _ := u.pages.GetFrontPage()
		if name != "main" {
			return e
		}
		if u.resizeLayout(e) {
			return nil
		}
		switch e.Key() {
		case tcell.KeyCtrlC:
			u.quit()
			return nil
		case tcell.KeyF1:
			u.showHelp()
			return nil
		case tcell.KeyF2:
			u.visual = !u.visual
			if u.visual {
				u.resultPages.SwitchToPage("visual")
				u.App.SetFocus(u.inspector.pages)
			} else {
				u.resultPages.SwitchToPage("raw")
				u.App.SetFocus(u.result)
			}
			return nil
		case tcell.KeyF3:
			u.editRequest()
			return nil
		case tcell.KeyF4:
			u.edit()
			return nil
		case tcell.KeyF5:
			u.execute()
			return nil
		case tcell.KeyF6:
			u.profiles()
			return nil
		case tcell.KeyF7:
			u.httpConsole()
			return nil
		case tcell.KeyF11:
			u.httpHistory()
			return nil
		case tcell.KeyF10:
			u.showUASubscriptions()
			return nil
		case tcell.KeyF9:
			u.uaHistory()
			return nil
		case tcell.KeyF8:
			u.stop()
			u.stopUASubscriptions()
			u.setStatus("正在取消…")
			return nil
		case tcell.KeyCtrlL:
			u.events = nil
			u.result.Clear()
			if u.lastRequest.Protocol != "" {
				u.inspector.reset(u.lastRequest)
			}
			return nil
		case tcell.KeyTab, tcell.KeyBacktab:
			items := []tview.Primitive{u.list, u.detail, u.resultPages, u.search}
			delta := 1
			if e.Key() == tcell.KeyBacktab {
				delta = 3
			}
			u.focus = (u.focus + delta) % 4
			u.App.SetFocus(items[u.focus])
			return nil
		}
		if !u.search.HasFocus() {
			switch e.Rune() {
			case '?':
				u.showHelp()
				return nil
			case 'q':
				u.quit()
				return nil
			}
		}
		return e
	})
	return u, nil
}
func (u *UI) setStatus(s string) {
	mode := ""
	if u.readonly {
		mode = " · 只读 READ ONLY"
	}
	u.status.SetText(clean(s) + "\n环境 Profile: " + u.profile + mode + " • " + filepath.Base(u.path))
}
func (u *UI) populate(filter string) {
	u.list.Clear()
	u.indexes = nil
	for i, r := range u.collection.Requests {
		if strings.Contains(strings.ToLower(r.ID+" "+r.Name+" "+r.Protocol), strings.ToLower(filter)) {
			u.indexes = append(u.indexes, i)
			name := r.Name
			if name == "" {
				name = r.ID
			}
			u.list.AddItem(display(name), display(strings.ToUpper(r.Protocol)+" · "+r.Action), 0, nil)
		}
	}
	if len(u.indexes) > 0 {
		u.selected = u.indexes[0]
		u.list.SetCurrentItem(0)
		u.preview()
	} else {
		u.detail.SetText("未找到匹配请求。按 F4 打开配置编辑器。")
	}
}
func (u *UI) preview() {
	if u.selected >= len(u.collection.Requests) {
		return
	}
	r := u.collection.Requests[u.selected]
	masked := redactPreview(r)
	b, e := json.MarshalIndent(masked, "", "  ")
	if e != nil {
		b, _ = yaml.Marshal(masked)
	}
	note := ""
	if r.Mutates() {
		note = "此操作会修改数据 · 执行前需要确认\n\n"
	}
	u.detail.SetText(clean(note + string(b)))
}
func (u *UI) modal(text string) {
	m := tview.NewModal().SetText(clean(text)).AddButtons([]string{"关闭"})
	m.SetDoneFunc(func(_ int, _ string) { u.pages.RemovePage("modal"); u.App.SetFocus(u.list) })
	m.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			u.pages.RemovePage("modal")
			u.App.SetFocus(u.list)
			return nil
		}
		return e
	})
	u.pages.AddPage("modal", m, true, true)
	u.App.SetFocus(m)
}
func (u *UI) execute() {
	if u.running {
		u.setStatus("已有请求正在执行，请先按 F8 取消。")
		return
	}
	if len(u.indexes) == 0 {
		return
	}
	u.navigation = nil
	original := u.collection.Requests[u.selected]
	if original.Protocol == "http" {
		if original.Mutates() && u.readonly {
			u.modal("只读模式禁止修改操作")
			return
		}
		u.start(original)
		return
	}
	r, e := u.collection.Resolve(u.collection.Requests[u.selected], u.profile)
	if e != nil {
		u.modal(e.Error())
		return
	}
	if r.Mutates() {
		u.confirmDerived(r)
		return
	}
	u.start(r)
}
func (u *UI) start(r config.Request) {
	u.mqttPreviewPending = nil
	u.uaCopyPending = false
	u.uaCopyValue = ""
	u.methodArgumentsPending = nil
	u.lastRequest = r
	u.inspector.reset(r)
	ctx, cancel := context.WithCancel(context.Background())
	u.mu.Lock()
	u.cancel = cancel
	u.mu.Unlock()
	u.running = true
	u.events = nil
	u.lastHTTPBody = nil
	u.result.Clear()
	u.setStatus("正在执行 " + r.ID + " · F8 取消")
	if strings.HasPrefix(r.Endpoint, "mock://") {
		u.setStatus("模拟设备（不连接真实设备）· " + r.ID + " · F8 取消")
	}
	go func() {
		ctx = engine.WithHTTPWorkflowOptions(ctx, engine.HTTPWorkflowOptions{HistoryPath: u.HTTPHistoryPath, AuthorizeRequestWrite: u.authorizeChainWrite, AuthorizeChainWrite: u.authorizeChainWrite, Prompt: u.workflowPrompt, Select: u.workflowSelect})
		e := engine.RunCollection(ctx, u.collection, r, u.profile, r.Mutates(), func(event engine.Event) {
			b, err := json.MarshalIndent(event, "", "  ")
			if err != nil {
				b = []byte(fmt.Sprintf("无法显示事件：%v", err))
			}
			s := clean(string(b))
			if len(s) > 32768 {
				s = s[:32768] + "\n… 界面已截断显示，请用命令行获取完整数据"
			}
			u.App.QueueUpdateDraw(func() {
				if event.Kind == "response" {
					if data, ok := event.Data.(map[string]any); ok {
						if raw, ok := data["raw_body_base64"].(string); ok {
							u.lastHTTPBody, _ = base64.StdEncoding.DecodeString(raw)
						}
					}
				}
				u.inspector.add(event)
				u.events = append(u.events, s)
				if len(u.events) > 128 {
					u.events = u.events[len(u.events)-128:]
				}
				u.updateResult(strings.Join(u.events, "\n\n"))
			})
		})
		cancel()
		u.App.QueueUpdateDraw(func() {
			u.running = false
			if u.quitting {
				u.mqttPreviewPending = nil
				if u.activeUASubscriptions() == 0 && len(u.localCancels) == 0 {
					u.App.Stop()
				}
				return
			}
			u.mu.Lock()
			u.cancel = nil
			u.mu.Unlock()
			if e != nil {
				u.setStatus("已停止：" + e.Error())
			} else {
				u.setStatus("执行完成：" + r.ID)
			}
			if e == nil {
				u.rememberUAConnection(u.lastRequest)
			}
			u.finishMQTTPreview(e == nil)
			if e == nil && u.methodArgumentsPending != nil {
				u.methodForm(u.methodArgumentsPending)
			}
			u.methodArgumentsPending = nil
			if e == nil && u.uaCopyPending {
				u.copyText(u.uaCopyValue)
			}
			u.uaCopyPending = false
		})
	}()
}
func (u *UI) stop() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cancel != nil {
		u.cancel()
	}
}
func (u *UI) edit() {
	if u.running {
		u.modal("请先取消当前请求，再编辑配置。")
		return
	}
	editor := tview.NewTextArea().SetText(string(u.raw), false)
	editor.SetBorder(true).SetTitle(" 请求配置 YAML · Ctrl-S 保存 · Esc 放弃 ")
	editor.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch e.Key() {
		case tcell.KeyEscape:
			u.pages.RemovePage("editor")
			u.App.SetFocus(u.list)
			return nil
		case tcell.KeyCtrlS:
			b := []byte(editor.GetText())
			current, err := os.ReadFile(u.path)
			if err != nil || !bytes.Equal(current, u.raw) {
				editor.SetTitle(" 文件已被外部修改，已停止覆盖 · Esc 关闭 ")
				return nil
			}
			if e := config.SaveChecked(u.path, b, func(data []byte) error { _, err := engine.ParseCollectionAt(data, u.path); return err }); e != nil {
				editor.SetTitle(" 保存失败：" + clean(e.Error()) + " · Esc 放弃 ")
				return nil
			}
			c, e := engine.ParseCollectionAt(b, u.path)
			if e != nil {
				return nil
			}
			c.SourcePath, _ = filepath.Abs(u.path)
			u.collection = c
			u.raw = b
			u.pages.RemovePage("editor")
			u.populate(u.search.GetText())
			u.App.SetFocus(u.list)
			u.setStatus("配置已保存")
			return nil
		}
		return e
	})
	u.pages.AddPage("editor", editor, true, true)
	u.App.SetFocus(editor)
}
func (u *UI) profiles() {
	if u.running {
		u.modal("请先取消当前请求，再切换环境。")
		return
	}
	names := []string{""}
	for name := range u.collection.Profiles {
		names = append(names, name)
	}
	sort.Strings(names[1:])
	list := tview.NewList()
	list.SetBorder(true).SetTitle(" 选择环境 · Esc 关闭 ")
	for _, name := range names {
		n := name
		label := n
		if n == "" {
			label = "（不使用环境）"
		}
		list.AddItem(label, "", 0, func() {
			u.profile = n
			u.pages.RemovePage("profiles")
			u.App.SetFocus(u.list)
			u.setStatus("已切换环境")
		})
	}
	list.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			u.pages.RemovePage("profiles")
			u.App.SetFocus(u.list)
			return nil
		}
		return e
	})
	u.pages.AddPage("profiles", list, true, true)
	u.App.SetFocus(list)
}
func (u *UI) Run() error { defer u.stop(); defer u.stopUASubscriptions(); return u.App.Run() }
func Run(path, profile string, readonly bool) error {
	if _, e := os.Stat(path); e != nil {
		return e
	}
	u, e := New(path, profile, readonly)
	if e != nil {
		return e
	}
	return u.Run()
}

func (u *UI) quit() {
	u.quitting = true
	u.stop()
	u.stopUASubscriptions()
	for _, cancel := range u.localCancels {
		cancel()
	}
	if !u.running && u.activeUASubscriptions() == 0 && len(u.localCancels) == 0 {
		u.App.Stop()
	}
}
