package tui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/linkedin/goavro/v2"
	"github.com/rivo/tview"
)

const kafkaEditorLimit = 64 << 10

var kafkaTopicName = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,249}$`)

func kafkaRequest(source config.Request, action string, params map[string]any) config.Request {
	r := copyRequest(source)
	r.Action = action
	// Bodies and selectors belong to the previous operation, not a new action.
	for _, key := range []string{"body", "json", "form_urlencoded", "form_multipart", "query", "configs", "confirm_subject", "permanent"} {
		delete(r.Params, key)
	}
	for key, value := range params {
		if value == nil {
			delete(r.Params, key)
		} else {
			r.Params[key] = value
		}
	}
	return r
}
func (u *UI) kafkaSource() config.Request {
	if u.inspector.kafka != nil {
		return u.inspector.kafka.request
	}
	return u.lastRequest
}
func (u *UI) kafkaRead(action string, params map[string]any) {
	if u.running {
		u.setStatus("请先等待当前请求结束，或按F8取消")
		return
	}
	source := u.kafkaSource()
	r := kafkaRequest(source, action, params)
	var err error
	r, err = u.collection.Resolve(r, u.profile)
	if err != nil {
		u.modal(err.Error())
		return
	}
	if r.Protocol != "kafka" || r.Mutates() {
		u.modal("此入口仅支持Kafka只读浏览")
		return
	}
	if action != source.Action || params != nil {
		u.kafkaRememberRead(source)
	}
	u.visual = true
	u.resultPages.SwitchToPage("visual")
	u.start(r)
	u.App.SetFocus(u.inspector.table)
}
func (u *UI) kafkaMutate(action string, params map[string]any) {
	if u.running || u.readonly {
		u.modal("当前正在执行或为只读模式，未执行修改")
		return
	}
	r := kafkaRequest(u.kafkaSource(), action, params)
	var err error
	r, err = u.collection.Resolve(r, u.profile)
	if err != nil {
		u.modal(err.Error())
		return
	}
	if !r.Mutates() {
		u.modal("未知修改操作，已拒绝执行")
		return
	}
	if action == "purge-subject" {
		form := tview.NewForm().AddInputField("输入完整subject确认", "", 64, nil, nil)
		close := func() { u.pages.RemovePage("kafka-purge"); u.App.SetFocus(u.inspector.table) }
		form.SetBorder(true).SetTitle(" 永久删除全部subject历史 · 不可恢复 · Esc取消 ")
		form.AddButton("检查并确认", func() {
			name := form.GetFormItem(0).(*tview.InputField).GetText()
			if name != r.String("subject", "") || name == "" {
				form.SetTitle(" Subject不匹配，未执行 ")
				return
			}
			r.Params["confirm_subject"] = name
			close()
			u.confirmDerived(r)
		}).AddButton("取消", close).SetCancelFunc(close)
		u.pages.AddPage("kafka-purge", form, true, true)
		u.App.SetFocus(form)
		return
	}
	u.confirmDerived(r)
}
func (u *UI) kafkaForm(name, title string, form *tview.Form, submit func() error, mutates bool) {
	if u.running {
		u.modal("请先等待读取完成，或先按F8取消")
		return
	}
	if mutates && u.readonly {
		u.modal("只读模式禁止修改操作")
		return
	}
	close := func() { u.pages.RemovePage(name); u.App.SetFocus(u.inspector.table) }
	button := "开始只读操作"
	if mutates {
		button = "检查并确认"
	}
	form.AddButton(button, func() {
		if err := submit(); err != nil {
			form.SetTitle(" " + display(err.Error()) + " · Esc取消 ")
			return
		}
		if front, _ := u.pages.GetFrontPage(); front == name {
			close()
		} else {
			u.pages.RemovePage(name)
		}
	}).AddButton("取消", close).SetCancelFunc(close)
	form.SetBorder(true).SetTitle(" " + title + " · Esc取消 ")
	u.pages.AddPage(name, form, true, true)
	u.App.SetFocus(form)
}
func kafkaInput(form *tview.Form, index int) string {
	return form.GetFormItem(index).(*tview.InputField).GetText()
}
func kafkaInteger(text, label string, min, max int) (int, error) {
	value, e := strconv.Atoi(strings.TrimSpace(text))
	if e != nil || value < min || value > max {
		return 0, fmt.Errorf("%s必须在%d..%d", label, min, max)
	}
	return value, nil
}
func kafkaValidateTopic(topic string) error {
	if !kafkaTopicName.MatchString(topic) || topic == "." || topic == ".." {
		return fmt.Errorf("主题应为1..249个字母、数字、点、下划线或连字符")
	}
	return nil
}
func kafkaStartTime(text string, now time.Time) (string, error) {
	now = now.UTC()
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch text {
	case "today":
		return start.Format(time.RFC3339), nil
	case "yesterday":
		return start.AddDate(0, 0, -1).Format(time.RFC3339), nil
	case "last7days":
		return now.AddDate(0, 0, -7).Format(time.RFC3339), nil
	}
	if _, e := time.Parse(time.RFC3339, text); e != nil {
		return "", fmt.Errorf("起始时间需RFC3339或today/yesterday/last7days")
	}
	return text, nil
}
func (u *UI) kafkaConsumeForm(topic string) {
	source := u.kafkaSource()
	form := tview.NewForm()
	offset := source.String("offset", "earliest")
	index := 0
	if offset == "latest" {
		index = 1
	}
	partitionText := source.String("partition", "")
	if ids, ok := source.Params["consume_partitions"].([]any); ok {
		parts := []string{}
		for _, id := range ids {
			parts = append(parts, fmt.Sprint(id))
		}
		partitionText = strings.Join(parts, ",")
	}
	form.AddInputField("主题", topic, 64, nil, nil).AddInputField("消息上限1..100000", strconv.Itoa(source.Int("limit", 100)), 12, nil, nil).AddDropDown("起始偏移", []string{"earliest", "latest"}, index, nil).AddInputField("时间(UTC/RFC3339/相对)", source.String("start_time", ""), 40, nil, nil).AddInputField("分区(逗号分隔/空=全部)", partitionText, 20, nil, nil).AddInputField("键包含", source.String("key_filter", ""), 45, nil, nil).AddInputField("键前缀", source.String("key_prefix", ""), 45, nil, nil).AddInputField("值包含", source.String("filter", ""), 45, nil, nil).AddInputField("值前缀", source.String("value_prefix", ""), 45, nil, nil).AddDropDown("键格式", []string{"auto", "text", "avro"}, 0, nil).AddDropDown("值格式", []string{"auto", "text", "avro"}, 0, nil)
	u.kafkaForm("kafka-consume", "Kafka消费条件 · 不提交组偏移", form, func() error {
		topic := kafkaInput(form, 0)
		if err := kafkaValidateTopic(topic); err != nil {
			return err
		}
		limit, err := kafkaInteger(kafkaInput(form, 1), "消息上限", 1, 100000)
		if err != nil {
			return err
		}
		start, err := kafkaStartTime(kafkaInput(form, 3), time.Now())
		if err != nil {
			return err
		}
		_, offset := form.GetFormItem(2).(*tview.DropDown).GetCurrentOption()
		_, keyFmt := form.GetFormItem(9).(*tview.DropDown).GetCurrentOption()
		_, valueFmt := form.GetFormItem(10).(*tview.DropDown).GetCurrentOption()
		params := map[string]any{"topic": topic, "limit": limit, "offset": offset, "start_time": start, "key_filter": kafkaInput(form, 5), "key_prefix": kafkaInput(form, 6), "filter": kafkaInput(form, 7), "value_prefix": kafkaInput(form, 8), "key_format": keyFmt, "value_format": valueFmt}
		partitions, err := kafkaPartitionInput(kafkaInput(form, 4))
		if err != nil {
			return err
		}
		params["partition"] = nil
		params["consume_partitions"] = nil
		if len(partitions) > 0 {
			params["consume_partitions"] = partitions
		}

		u.kafkaRead("consume", params)
		return nil
	}, false)
}
func (u *UI) kafkaTopicForm(action, topic string) {
	form := tview.NewForm().AddInputField("主题", topic, 64, nil, nil).AddInputField("目标分区总数", "1", 12, nil, nil)
	if action == "create-topic" {
		form.AddInputField("副本数", "1", 12, nil, nil)
	}
	u.kafkaForm("kafka-topic", "Kafka主题管理 · 扩分区不可撤销", form, func() error {
		topic := kafkaInput(form, 0)
		if e := kafkaValidateTopic(topic); e != nil {
			return e
		}
		partitions, e := kafkaInteger(kafkaInput(form, 1), "分区数", 1, 100000)
		if e != nil {
			return e
		}
		params := map[string]any{"topic": topic, "partitions": partitions}
		if action == "create-topic" {
			replicas, e := kafkaInteger(kafkaInput(form, 2), "副本数", 1, 32767)
			if e != nil {
				return e
			}
			params["replication_factor"] = replicas
		}
		u.kafkaMutate(action, params)
		return nil
	}, true)
}
func (u *UI) kafkaProduceForm(topic string) {
	form := tview.NewForm().AddInputField("主题", topic, 64, nil, nil).AddInputField("键", "", 64, nil, nil).AddDropDown("值格式", []string{"text", "avro"}, 0, nil).AddInputField("Avro subject(如适用)", "", 64, nil, nil).AddInputField("Avro版本", "latest", 12, nil, nil).AddTextArea("值", "", 70, 12, kafkaEditorLimit, nil)
	u.kafkaForm("kafka-produce", "Kafka生产一条消息", form, func() error {
		topic := kafkaInput(form, 0)
		if e := kafkaValidateTopic(topic); e != nil {
			return e
		}
		_, format := form.GetFormItem(2).(*tview.DropDown).GetCurrentOption()
		params := map[string]any{"topic": topic, "key": kafkaInput(form, 1), "key_format": "text", "value_format": format, "value": form.GetFormItem(5).(*tview.TextArea).GetText()}
		if format == "avro" {
			subject := strings.TrimSpace(kafkaInput(form, 3))
			if subject == "" {
				return fmt.Errorf("Avro必须指定subject")
			}
			version := kafkaInput(form, 4)
			if version != "latest" {
				if _, e := kafkaInteger(version, "Avro版本", 1, 2147483647); e != nil {
					return e
				}
			}
			params["value_subject"] = subject
			params["value_version"] = version
		}
		u.kafkaMutate("produce", params)
		return nil
	}, true)
}
func (u *UI) kafkaSchemaForm(subject string) {
	form := tview.NewForm().AddInputField("Subject", subject, 64, nil, nil).AddTextArea("Avro schema JSON", `{"type":"record","name":"Event","fields":[]}`, 75, 18, kafkaEditorLimit, nil)
	u.kafkaForm("kafka-schema", "注册Avro schema新版本", form, func() error {
		subject := strings.TrimSpace(kafkaInput(form, 0))
		if subject == "" || len(subject) > 1024 {
			return fmt.Errorf("subject长度需1..1024")
		}
		schema := form.GetFormItem(1).(*tview.TextArea).GetText()
		if len(schema) > kafkaEditorLimit {
			return fmt.Errorf("TUI schema编辑上限64KiB")
		}
		if _, err := goavro.NewCodec(schema); err != nil {
			return fmt.Errorf("Avro schema无效，请检查字段与类型")
		}
		u.kafkaMutate("register-schema", map[string]any{"subject": subject, "json": map[string]any{"schemaType": "AVRO", "schema": schema}})
		return nil
	}, true)
}
func kafkaConfigJSON(text string) (map[string]any, error) {
	if len(text) > kafkaEditorLimit {
		return nil, fmt.Errorf("TUI配置编辑上限64KiB")
	}
	var raw any
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	if err := decodeSingle(d, &raw); err != nil {
		return nil, fmt.Errorf("配置必须是单个JSON对象")
	}
	values, ok := raw.(map[string]any)
	if !ok || len(values) == 0 {
		return nil, fmt.Errorf("配置对象不能为空")
	}
	for key, value := range values {
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("配置名不能为空")
		}
		if _, ok := value.(string); !ok {
			return nil, fmt.Errorf("Kafka配置值必须是字符串")
		}
	}
	return values, nil
}
func (u *UI) kafkaConfigForm(action, name string, values map[string]any) {
	body, err := json.MarshalIndent(values, "", "  ")
	if err != nil || len(body) > kafkaEditorLimit {
		u.modal("完整配置超过64KiB TUI编辑上限，未截断配置；请使用请求配置文件")
		return
	}
	title := "编辑所选主题配置项"
	if action == "update-connector" {
		title = "更新连接器完整配置 · 未出现的键可能被移除"
	}
	form := tview.NewForm().AddInputField("目标", name, 64, nil, nil).AddTextArea("配置JSON(值为字符串)", string(body), 75, 18, kafkaEditorLimit, nil)
	u.kafkaForm("kafka-config", title, form, func() error {
		target := strings.TrimSpace(kafkaInput(form, 0))
		if target == "" || len(target) > 1024 {
			return fmt.Errorf("目标名称长度需1..1024")
		}
		cfg, err := kafkaConfigJSON(form.GetFormItem(1).(*tview.TextArea).GetText())
		if err != nil {
			return err
		}
		params := map[string]any{"topic": target, "configs": cfg}
		if action == "update-connector" {
			params = map[string]any{"connector": target, "json": cfg}
		}
		u.kafkaMutate(action, params)
		return nil
	}, true)
}

func kafkaPartitionInput(text string) ([]any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	terms := strings.Split(text, ",")
	if len(terms) > 1024 {
		return nil, fmt.Errorf("最多1024个分区")
	}
	out := make([]any, 0, len(terms))
	seen := map[int]bool{}
	for _, term := range terms {
		n, e := kafkaInteger(term, "分区", 0, 2147483647)
		if e != nil {
			return nil, e
		}
		if seen[n] {
			return nil, fmt.Errorf("分区不能重复")
		}
		seen[n] = true
		out = append(out, n)
	}
	return out, nil
}

// Back navigation must never replay writes, including an earlier confirmed write.
func (u *UI) kafkaRememberRead(source config.Request) {
	if source.Mutates() {
		return
	}
	if len(u.navigation) >= 32 {
		u.navigation = u.navigation[1:]
	}
	u.navigation = append(u.navigation, copyRequest(source))
}
