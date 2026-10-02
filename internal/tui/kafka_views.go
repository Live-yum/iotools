package tui

// Protocol-specific Kafka views. No upstream UI implementation is embedded.
import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/twmb/franz-go/pkg/kadm"
)

const kafkaMaxRows = 1000
const kafkaMaxRecords = 128
const kafkaDetailLimit = 32 << 10

type kafkaRow struct {
	cells []string
	ref   any
}
type kafkaItem struct {
	kind, name string
	version    string
	data       any
}
type kafkaView struct {
	request      config.Request
	title        string
	headers      []string
	rows         []kafkaRow
	filter       string
	sortColumn   int
	descending   bool
	hideInternal bool
}

func (v *inspector) kafkaReset(r config.Request) {
	if r.Protocol != "kafka" {
		v.kafka = nil
		return
	}
	v.kafka = &kafkaView{request: copyRequest(r), title: "Kafka · r 刷新 · / 筛选 · > 排序 · 退格返回", headers: []string{"等待结果"}}
	v.kafkaRender()
}
func (v *inspector) kafkaTable(title string, headers ...string) {
	if v.kafka == nil {
		v.kafkaReset(v.owner.lastRequest)
	}
	if v.kafka == nil {
		return
	}
	v.kafka.title = title
	v.kafka.headers = headers
	v.kafka.rows = nil
	v.kafka.filter = ""
	v.kafka.sortColumn = 0
	v.kafka.descending = false
}
func (v *inspector) kafkaRow(ref any, values ...any) {
	if len(v.kafka.rows) >= kafkaMaxRows {
		return
	}
	cells := make([]string, len(values))
	for i, x := range values {
		cells[i] = kafkaShort(fmt.Sprint(x), 512)
	}
	v.kafka.rows = append(v.kafka.rows, kafkaRow{cells, ref})
}
func kafkaShort(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…（显示已截断）"
	}
	return s
}
func kafkaError(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
func (v *inspector) kafkaRender() {
	if v.kafka == nil {
		return
	}
	k := v.kafka
	v.table.Clear()
	v.pages.SwitchToPage("table")
	title := k.title + " · /筛选 >列排序 =反向 r刷新"
	if k.filter != "" {
		title += " · 筛选: " + kafkaShort(k.filter, 40)
	}
	if len(k.rows) >= kafkaMaxRows {
		title += " · 仅前1000行"
	}
	v.table.SetTitle(" " + display(title) + " ")
	for i, h := range k.headers {
		v.table.SetCell(0, i, tview.NewTableCell(h).SetSelectable(false).SetTextColor(tcell.ColorAqua))
	}
	rows := append([]kafkaRow(nil), k.rows...)
	sort.SliceStable(rows, func(i, j int) bool {
		c := k.sortColumn
		if c >= len(rows[i].cells) || c >= len(rows[j].cells) {
			return false
		}
		a, b := rows[i].cells[c], rows[j].cells[c]
		less := a < b
		ai, ae := strconv.ParseInt(a, 10, 64)
		bi, be := strconv.ParseInt(b, 10, 64)
		if ae == nil && be == nil {
			less = ai < bi
		}
		if k.descending {
			return a != b && !less
		}
		return less
	})
	row := 1
	for _, r := range rows {
		if k.filter != "" && !strings.Contains(strings.ToLower(strings.Join(r.cells, " ")), strings.ToLower(k.filter)) {
			continue
		}
		if k.hideInternal && k.request.Action == "topics" && len(r.cells) > 3 && r.cells[3] == "true" {
			continue
		}
		for col, s := range r.cells {
			v.table.SetCell(row, col, tview.NewTableCell(display(s)).SetReference(r.ref))
		}
		row++
	}
	if row > 1 {
		v.table.Select(1, 0)
	}
}
func (v *inspector) kafkaEvent(e engine.Event) bool {
	if v.protocol != "kafka" || v.kafka == nil {
		return false
	}
	switch data := e.Data.(type) {
	case kadm.TopicDetails:
		if e.Kind != "topics" {
			return false
		}
		v.kafkaTable("Kafka 主题 · Enter消费 c条件 p生产 o配置 n新建 D删除 i内部主题", "主题", "分区数", "副本数", "内部主题", "错误")
		names := make([]string, 0, len(data))
		for name := range data {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			d := data[name]
			replicas := 0
			for _, p := range d.Partitions {
				if len(p.Replicas) > replicas {
					replicas = len(p.Replicas)
				}
			}
			v.kafkaRow(name, name, len(d.Partitions), replicas, d.IsInternal, kafkaError(d.Err))
		}
	case kadm.BrokerDetails:
		v.kafkaTable("Kafka brokers", "ID", "主机", "端口", "机架")
		for _, b := range data {
			rack := ""
			if b.Rack != nil {
				rack = *b.Rack
			}
			v.kafkaRow(nil, b.NodeID, b.Host, b.Port, rack)
		}
	case kadm.ListedGroups:
		v.kafkaTable("Kafka 消费组 · Enter成员 l延迟 D删除", "消费组", "状态", "协议", "协调者")
		for _, g := range data.Sorted() {
			v.kafkaRow(kafkaItem{kind: "group", name: g.Group}, g.Group, g.State, g.ProtocolType, g.Coordinator)
		}
	case kadm.DescribedGroups:
		v.kafkaTable("Kafka 组成员 · l偏移与延迟 · 退格返回", "消费组", "状态", "成员ID", "客户端", "主机", "分配", "错误")
		for _, g := range data.Sorted() {
			if len(g.Members) == 0 {
				v.kafkaRow(kafkaItem{kind: "group", name: g.Group}, g.Group, g.State, "（无成员）", "", "", "", kafkaError(g.Err))
			}
			for _, m := range g.Members {
				parts := []string{}
				if a, ok := m.Assigned.AsConsumer(); ok {
					for _, t := range a.Topics {
						parts = append(parts, fmt.Sprintf("%s:%v", t.Topic, t.Partitions))
					}
				}
				v.kafkaRow(kafkaItem{kind: "group", name: g.Group}, g.Group, g.State, m.MemberID, m.ClientID, m.ClientHost, strings.Join(parts, "; "), kafkaError(g.Err))
			}
		}
	case kadm.DescribedGroupLags:
		v.kafkaTable("Kafka 消费延迟 · Enter组成员 · 退格返回", "消费组", "主题", "分区", "已提交", "起始", "末端", "延迟", "客户端", "错误")
		for _, g := range data.Sorted() {
			if err := g.Error(); err != nil {
				v.kafkaRow(kafkaItem{kind: "group", name: g.Group}, g.Group, "", "", "", "", "", "", "", err)
				continue
			}
			if len(g.Lag) == 0 {
				v.kafkaRow(kafkaItem{kind: "group", name: g.Group}, g.Group, "（无偏移）", "", "", "", "", "", "", "")
			}
			for _, p := range g.Lag.Sorted() {
				client := ""
				if p.Member != nil {
					client = p.Member.ClientID
				}
				v.kafkaRow(kafkaItem{kind: "group", name: g.Group}, g.Group, p.Topic, p.Partition, p.Commit.At, p.Start.Offset, p.End.Offset, p.Lag, client, kafkaError(p.Err))
			}
		}
	case kadm.ListedOffsets:
		v.kafkaTable("Kafka 分区偏移 · c消费 · 退格返回", "主题", "分区", "末端偏移", "Leader epoch", "错误")
		for topic, partitions := range data {
			ids := make([]int, 0, len(partitions))
			for id := range partitions {
				ids = append(ids, int(id))
			}
			sort.Ints(ids)
			for _, id := range ids {
				p := partitions[int32(id)]
				v.kafkaRow(kafkaItem{kind: "topic", name: topic}, topic, id, p.Offset, p.LeaderEpoch, kafkaError(p.Err))
			}
		}
	case kadm.ResourceConfigs:
		v.kafkaTable("Kafka 主题配置 · e编辑所选项 +扩展分区 · 退格返回", "主题", "配置项", "值", "来源", "错误")
		for _, topic := range data {
			if topic.Err != nil {
				v.kafkaRow(nil, topic.Name, "", "", "", topic.Err)
				continue
			}
			for _, c := range topic.Configs {
				value := c.MaybeValue()
				var ref any = kafkaItem{kind: "config", name: topic.Name, data: map[string]any{c.Key: value}}
				if c.Sensitive {
					value = "（敏感值未返回）"
					ref = nil
				}
				v.kafkaRow(ref, topic.Name, c.Key, value, c.Source, "")
			}
		}
	case map[string]any:
		switch e.Kind {
		case "record":
			if len(v.kafka.headers) == 1 {
				v.kafkaTable("Kafka 消息 · Enter详情 c重设消费条件 · F8停止", "主题", "分区", "偏移", "时间", "键", "值")
			}
			if len(v.kafka.rows) >= kafkaMaxRecords {
				v.kafka.rows = v.kafka.rows[1:]
			}
			b, _ := json.MarshalIndent(data, "", "  ")
			v.kafkaRow(kafkaItem{kind: "record", data: kafkaShort(string(b), kafkaDetailLimit)}, data["topic"], data["partition"], data["offset"], data["timestamp"], kafkaJSON(data["key"]), kafkaJSON(data["value"]))
		case "response":
			return v.kafkaResponse(data)
		default:
			if !v.kafka.request.Mutates() {
				return false
			}
			v.kafkaTable("Kafka操作结果 · r重新查询服务器", "操作", "结果")
			v.kafkaRow(nil, v.kafka.request.Action, kafkaJSON(data))
		}
	default:
		if v.kafka.request.Mutates() {
			v.kafkaTable("Kafka操作结果 · r重新查询服务器", "操作", "结果")
			v.kafkaRow(nil, v.kafka.request.Action, kafkaJSON(e.Data))
		} else {
			return false
		}
	}
	v.kafkaRender()
	return true
}
func kafkaJSON(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
func (v *inspector) kafkaResponse(response map[string]any) bool {
	r := v.kafka.request
	if status, ok := response["status"].(int); ok && (status < 200 || status >= 300) {
		return false
	}
	body := response["body"]
	switch r.Action {
	case "schemas":
		subjects, ok := body.([]any)
		if !ok {
			return false
		}
		v.kafkaTable("Schema subjects · Enter版本 n注册 D软删除 H永久删除", "Subject")
		for _, s := range subjects {
			if name, ok := s.(string); ok {
				v.kafkaRow(kafkaItem{kind: "subject", name: name}, name)
			}
		}
	case "schema-versions":
		versions, ok := body.([]any)
		if !ok {
			return false
		}
		v.kafkaTable("Schema 版本 · Enter查看 n注册 D删除版本", "Subject", "版本")
		for _, version := range versions {
			v.kafkaRow(kafkaItem{kind: "version", name: r.String("subject", ""), data: fmt.Sprint(version)}, r.String("subject", ""), version)
		}
	case "schema":
		m, ok := body.(map[string]any)
		if !ok {
			return false
		}
		schema, _ := m["schema"].(string)
		v.kafkaTable("Avro Schema · Enter展开 n注册新版本 · 退格返回", "Subject", "版本", "ID", "类型", "Schema")
		v.kafkaRow(kafkaItem{kind: "schema", name: r.String("subject", ""), version: fmt.Sprint(m["version"]), data: kafkaShort(schema, 256<<10)}, m["subject"], m["version"], m["id"], m["schemaType"], schema)
	case "connectors":
		m, ok := body.(map[string]any)
		if !ok {
			return false
		}
		v.kafkaTable("Kafka Connect · Enter任务 e配置 P暂停 R恢复 D删除", "连接器", "状态", "类型", "任务数", "Worker")
		names := make([]string, 0, len(m))
		for name := range m {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			entry, _ := m[name].(map[string]any)
			status, _ := entry["status"].(map[string]any)
			connector, _ := status["connector"].(map[string]any)
			tasks, _ := status["tasks"].([]any)
			info, _ := entry["info"].(map[string]any)
			cfg, _ := info["config"].(map[string]any)
			v.kafkaRow(kafkaItem{kind: "connector", name: name, data: cfg}, name, connector["state"], status["type"], len(tasks), connector["worker_id"])
		}
	case "connector":
		m, ok := body.(map[string]any)
		if !ok {
			return false
		}
		name := r.String("connector", "")
		connector, _ := m["connector"].(map[string]any)
		v.kafkaTable("Connect 状态 · P暂停 R恢复 D删除 · 退格返回", "连接器", "任务", "状态", "Worker", "详情")
		ref := kafkaItem{kind: "connector", name: name}
		v.kafkaRow(ref, name, "connector", connector["state"], connector["worker_id"], connector["trace"])
		tasks, _ := m["tasks"].([]any)
		for _, task := range tasks {
			if t, ok := task.(map[string]any); ok {
				v.kafkaRow(ref, name, t["id"], t["state"], t["worker_id"], t["trace"])
			}
		}
	default:
		if !r.Mutates() {
			return false
		}
		v.kafkaTable("Kafka HTTP操作完成 · r重新查询状态", "操作", "HTTP状态", "响应")
		v.kafkaRow(nil, r.Action, response["status"], kafkaJSON(body))
	}
	v.kafkaRender()
	return true
}

func (v *inspector) kafkaSelected() any {
	row, _ := v.table.GetSelection()
	if row < 1 || row >= v.table.GetRowCount() {
		return nil
	}
	return v.table.GetCell(row, 0).GetReference()
}
func (v *inspector) kafkaSelect(row, col int) {
	if v.kafka == nil || row < 1 || row >= v.table.GetRowCount() {
		return
	}
	ref := v.table.GetCell(row, 0).GetReference()
	switch x := ref.(type) {
	case string:
		v.owner.kafkaConsumeForm(x)
	case kafkaItem:
		switch x.kind {
		case "topic":
			v.owner.kafkaConsumeForm(x.name)
		case "group":
			v.owner.kafkaRead("group", map[string]any{"group": x.name})
		case "subject":
			v.owner.kafkaRead("schema-versions", map[string]any{"subject": x.name})
		case "version":
			v.owner.kafkaRead("schema", map[string]any{"subject": x.name, "version": x.data})
		case "connector":
			v.owner.kafkaRead("connector", map[string]any{"connector": x.name})
		case "record", "schema":
			v.kafkaDetail(x, row)
		}
	}
}
func (v *inspector) kafkaKey(e *tcell.EventKey) *tcell.EventKey {
	if v.protocol != "kafka" || v.kafka == nil {
		return e
	}
	u := v.owner
	k := v.kafka
	if e.Key() == tcell.KeyBackspace || e.Key() == tcell.KeyBackspace2 {
		u.back()
		return nil
	}
	switch e.Rune() {
	case '/':
		v.kafkaSearch()
		return nil
	case '>':
		k.sortColumn = (k.sortColumn + 1) % len(k.headers)
		v.kafkaRender()
		return nil
	case '=':
		k.descending = !k.descending
		v.kafkaRender()
		return nil
	case 'r':
		u.kafkaRead(kafkaReadAction(k.request.Action), nil)
		return nil
	}
	ref := v.kafkaSelected()
	name := ""
	var item kafkaItem
	switch x := ref.(type) {
	case string:
		name = x
	case kafkaItem:
		item = x
		name = x.name
	}
	switch k.request.Action {
	case "topics", "offsets", "topic-config", "consume":
		if name == "" {
			name = k.request.String("topic", "")
		}
		switch e.Rune() {
		case 'i':
			k.hideInternal = !k.hideInternal
			v.kafkaRender()
			return nil
		case 'n':
			u.kafkaTopicForm("create-topic", "")
			return nil
		case 'c':
			if name != "" {
				u.kafkaConsumeForm(name)
			}
			return nil
		case 'p':
			if name != "" {
				u.kafkaProduceForm(name)
			}
			return nil
		case 'o':
			if name != "" {
				u.kafkaRead("topic-config", map[string]any{"topic": name})
			}
			return nil
		case 'l':
			if name != "" {
				u.kafkaRead("offsets", map[string]any{"topic": name})
			}
			return nil
		case '+':
			if name != "" {
				u.kafkaTopicForm("expand-partitions", name)
			}
			return nil
		case 'e':
			if item.kind == "config" {
				cfg, _ := item.data.(map[string]any)
				u.kafkaConfigForm("alter-topic", name, cfg)
			}
			return nil
		case 'D':
			if name != "" {
				u.kafkaMutate("delete-topic", map[string]any{"topic": name})
			}
			return nil
		}
	case "groups", "group", "lag":
		if name == "" {
			name = k.request.String("group", "")
		}
		if name != "" {
			switch e.Rune() {
			case 'l':
				u.kafkaRead("lag", map[string]any{"groups": []any{name}})
				return nil
			case 'D':
				u.kafkaMutate("delete-group", map[string]any{"group": name})
				return nil
			}
		}
	case "schemas", "schema-versions", "schema":
		if name == "" {
			name = k.request.String("subject", "")
		}
		switch e.Rune() {
		case 'n':
			u.kafkaSchemaForm(name)
			return nil
		case 'D':
			if name != "" {
				action := "delete-subject"
				params := map[string]any{"subject": name}
				if item.kind == "version" || item.kind == "schema" {
					action = "delete-schema"
					params["version"] = item.data
					if item.kind == "schema" {
						params["version"] = item.version
					}
				}
				u.kafkaMutate(action, params)
			}
			return nil
		case 'H':
			if name != "" {
				u.kafkaMutate("purge-subject", map[string]any{"subject": name})
			}
			return nil
		}
	case "connectors", "connector":
		if name == "" {
			name = k.request.String("connector", "")
		}
		if name != "" {
			switch e.Rune() {
			case 'e':
				cfg, ok := item.data.(map[string]any)
				if !ok || len(cfg) == 0 {
					u.modal("请返回连接器列表，刷新完整配置后再编辑，避免覆盖未知配置")
				} else {
					u.kafkaConfigForm("update-connector", name, cfg)
				}
				return nil
			case 'P':
				u.kafkaMutate("pause-connector", map[string]any{"connector": name})
				return nil
			case 'R':
				u.kafkaMutate("resume-connector", map[string]any{"connector": name})
				return nil
			case 'D':
				u.kafkaMutate("delete-connector", map[string]any{"connector": name})
				return nil
			}
		}
	}
	return e
}
func (v *inspector) kafkaSearch() {
	form := tview.NewForm().AddInputField("筛选当前表格", v.kafka.filter, 50, nil, nil)
	close := func() { v.owner.pages.RemovePage("kafka-search"); v.owner.App.SetFocus(v.table) }
	form.AddButton("应用", func() { v.kafka.filter = form.GetFormItem(0).(*tview.InputField).GetText(); v.kafkaRender(); close() }).AddButton("清除", func() { v.kafka.filter = ""; v.kafkaRender(); close() }).AddButton("取消", close).SetCancelFunc(close)
	form.SetBorder(true).SetTitle(" Kafka 筛选 · Esc取消 ")
	v.owner.pages.AddPage("kafka-search", form, true, true)
	v.owner.App.SetFocus(form)
}
func (v *inspector) kafkaDetail(item kafkaItem, row int) {
	view := tview.NewTextView().SetText(clean(fmt.Sprint(item.data))).SetWrap(true).SetScrollable(true)
	view.SetBorder(true).SetTitle(" Kafka " + display(item.kind) + " · Ctrl-N/P上一/下一 · Ctrl-Y复制 · Esc返回 ")
	close := func() { v.owner.pages.RemovePage("kafka-detail"); v.owner.App.SetFocus(v.table) }
	view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyCtrlY {
			v.owner.copyText(fmt.Sprint(item.data))
			return nil
		}
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		next := row
		if e.Key() == tcell.KeyCtrlN {
			next++
		}
		if e.Key() == tcell.KeyCtrlP {
			next--
		}
		if next != row && next > 0 && next < v.table.GetRowCount() {
			if x, ok := v.table.GetCell(next, 0).GetReference().(kafkaItem); ok && x.kind == item.kind {
				v.kafkaDetail(x, next)
			}
			return nil
		}
		return e
	})
	v.owner.pages.AddPage("kafka-detail", view, true, true)
	v.owner.App.SetFocus(view)
}

func kafkaReadAction(action string) string {
	switch action {
	case "create-topic", "delete-topic", "alter-topic", "expand-partitions", "produce":
		return "topics"
	case "delete-group":
		return "groups"
	case "register-schema", "delete-schema", "delete-subject", "purge-subject":
		return "schemas"
	case "update-connector", "pause-connector", "resume-connector", "delete-connector":
		return "connectors"
	default:
		return action
	}
}
