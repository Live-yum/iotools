package tui

import (
	"encoding/json"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
)

type uaConnectionRecord struct {
	Endpoint      string            `json:"endpoint"`
	RequestID     string            `json:"request_id"`
	Profile       string            `json:"profile"`
	NodeID        string            `json:"node_id"`
	Preferences   map[string]string `json:"preferences"`
	AllowInsecure bool              `json:"allow_insecure"`
	Updated       time.Time         `json:"updated"`
}
type uaConnectionHistory struct {
	Version int                  `json:"version"`
	Records []uaConnectionRecord `json:"records"`
}

var uaPreferenceKeys = []string{"security_policy", "security_mode", "auth", "username", "cert_file", "key_file", "auth_cert_file", "auth_key_file", "server_cert_sha256", "ca_file"}

func uaHistoryPath(path string) string { return path + ".tui-state.json" }
func loadUAHistory(path string) (uaConnectionHistory, error) {
	empty := uaConnectionHistory{Version: 1, Records: []uaConnectionRecord{}}
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return empty, nil
	}
	if e != nil {
		return empty, e
	}
	if !info.Mode().IsRegular() {
		return empty, fmt.Errorf("连接历史必须是普通文件")
	}
	f, e := os.Open(path)
	if e != nil {
		return empty, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil {
		return empty, e
	}
	if len(b) > 1<<20 {
		return empty, fmt.Errorf("连接历史超过1MiB")
	}
	var h uaConnectionHistory
	if e = json.Unmarshal(b, &h); e != nil {
		return empty, e
	}
	if h.Version != 1 || len(h.Records) > 100 {
		return empty, fmt.Errorf("连接历史版本或数量无效")
	}
	return h, nil
}
func (u *UI) rememberUAConnection(r config.Request) {
	if r.Protocol != "opcua" || r.Action == "discover" || r.Mutates() {
		return
	}
	endpoint, e := url.Parse(r.Endpoint)
	if e != nil || endpoint.Scheme != "opc.tcp" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return
	}
	h, e := loadUAHistory(uaHistoryPath(u.path))
	if e != nil {
		u.setStatus("连接成功；历史未保存：" + e.Error())
		return
	}
	preferences := map[string]string{}
	for _, key := range uaPreferenceKeys {
		if value := r.String(key, ""); value != "" && len(value) <= 4096 {
			preferences[key] = value
		}
	}
	entry := uaConnectionRecord{r.Endpoint, r.ID, u.profile, r.String("node_id", "i=85"), preferences, r.Bool("allow_insecure"), time.Now().UTC()}
	kept := []uaConnectionRecord{entry}
	for _, old := range h.Records {
		if old.Endpoint != entry.Endpoint || old.Profile != entry.Profile {
			kept = append(kept, old)
		}
		if len(kept) == 100 {
			break
		}
	}
	h.Records = kept
	b, e := json.MarshalIndent(h, "", "  ")
	if e == nil {
		e = config.SaveChecked(uaHistoryPath(u.path), b, func(data []byte) error { var v uaConnectionHistory; return json.Unmarshal(data, &v) })
	}
	if e != nil {
		u.setStatus("连接成功；历史未保存：" + e.Error())
	}
}
func (u *UI) uaHistory() {
	if u.running {
		u.modal("请先按F8断开当前订阅")
		return
	}
	h, e := loadUAHistory(uaHistoryPath(u.path))
	if e != nil {
		u.modal("无法读取连接历史：" + e.Error())
		return
	}
	list := tview.NewList().ShowSecondaryText(true)
	list.SetBorder(true).SetTitle(" OPC UA 成功连接历史 · Enter 编辑后连接 · 不保存密码 · Esc关闭 ")
	close := func() { u.pages.RemovePage("ua-history"); u.App.SetFocus(u.list) }
	for _, record := range h.Records {
		entry := record
		list.AddItem(clean(entry.Endpoint), clean(entry.Profile+" · "+entry.NodeID+" · "+entry.Updated.Format("2006-01-02 15:04Z")), 0, func() {
			var source *config.Request
			for _, r := range u.collection.Requests {
				if r.ID == entry.RequestID && r.Protocol == "opcua" {
					c := copyRequest(r)
					source = &c
					break
				}
			}
			if source == nil {
				u.modal("原请求已不存在，请在F4恢复或新建连接请求")
				return
			}
			if entry.Profile != "" {
				if _, ok := u.collection.Profiles[entry.Profile]; !ok {
					u.modal("原环境已不存在")
					return
				}
			}
			u.profile = entry.Profile
			source.Endpoint = entry.Endpoint
			source.Params["node_id"] = entry.NodeID
			for key, value := range entry.Preferences {
				allowed := false
				for _, name := range uaPreferenceKeys {
					if key == name {
						allowed = true
						break
					}
				}
				if allowed {
					source.Params[key] = value
				}
			}
			source.Params["allow_insecure"] = entry.AllowInsecure
			delete(source.Params, "password")
			close()
			u.uaConnectionForm(*source, nil)
		})
	}
	if len(h.Records) == 0 {
		list.AddItem("尚无成功连接，选择发现或浏览请求后执行", "", 0, close)
	}
	list.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		return e
	})
	u.pages.AddPage("ua-history", list, true, true)
	u.App.SetFocus(list)
}
func (u *UI) uaConnectionForm(source config.Request, discovered map[string]any) {
	if u.running {
		u.setStatus("请等待发现结束")
		return
	}
	r := copyRequest(source)
	r.Action = "browse"
	delete(r.Params, "node_ids")
	delete(r.Params, "browse_path")
	if discovered != nil {
		r.Endpoint = fmt.Sprint(discovered["url"])
		r.Params["security_policy"] = strings.TrimPrefix(fmt.Sprint(discovered["security_policy"]), "http://opcfoundation.org/UA/SecurityPolicy#")
		r.Params["security_mode"] = fmt.Sprint(discovered["security_mode"])
		delete(r.Params, "password")
	}
	form := tview.NewForm().SetItemPadding(0)
	form.SetBorder(true).SetTitle(" OPC UA 连接 · 证书指纹须独立核实 · 不自动信任发现结果 ")
	labels := []string{"端点", "安全策略", "安全模式", "身份anonymous/username/certificate", "用户名", "密码/环境引用", "客户端证书", "客户端私钥", "服务器指纹（独立核实）", "CA文件", "用户证书", "用户私钥", "起始NodeId"}
	values := []string{r.Endpoint, r.String("security_policy", "Basic256Sha256"), r.String("security_mode", "SignAndEncrypt"), r.String("auth", "anonymous"), r.String("username", ""), r.String("password", ""), r.String("cert_file", ""), r.String("key_file", ""), r.String("server_cert_sha256", ""), r.String("ca_file", ""), r.String("auth_cert_file", ""), r.String("auth_key_file", ""), r.String("node_id", "i=85")}
	for i, label := range labels {
		form.AddInputField(label, values[i], 0, nil, nil)
	}
	form.GetFormItem(5).(*tview.InputField).SetMaskCharacter('*')
	insecure := r.Bool("allow_insecure")
	form.AddCheckbox("明确允许匿名None（隔离模拟器）", insecure, func(v bool) { insecure = v })
	close := func() { u.pages.RemovePage("ua-connect"); u.App.SetFocus(u.inspector.tree) }
	form.AddButton("连接并浏览", func() {
		get := func(i int) string { return form.GetFormItem(i).(*tview.InputField).GetText() }
		r.Endpoint = get(0)
		keys := []string{"security_policy", "security_mode", "auth", "username", "password", "cert_file", "key_file", "server_cert_sha256", "ca_file", "auth_cert_file", "auth_key_file", "node_id"}
		for i, key := range keys {
			if value := get(i + 1); value != "" {
				r.Params[key] = value
			} else {
				delete(r.Params, key)
			}
		}
		r.Params["allow_insecure"] = insecure
		resolved, e := u.collection.Resolve(r, u.profile)
		if e != nil {
			form.SetTitle("配置错误：" + display(e.Error()))
			return
		}
		close()
		u.start(resolved)
	})
	form.AddButton("取消", close).SetCancelFunc(close)
	u.pages.AddPage("ua-connect", form, true, true)
	u.App.SetFocus(form)
}
func (u *UI) uaPathForm() {
	if u.running {
		u.setStatus("请先等待当前请求结束或F8取消")
		return
	}
	if u.lastRequest.Protocol != "opcua" {
		return
	}
	form := tview.NewForm().SetItemPadding(0)
	form.SetBorder(true).SetTitle(" 浏览路径 · 从Root开始 · ns=N:名称 · &转义 · Esc取消 ")
	form.AddInputField("路径", "/Objects", 0, nil, nil)
	close := func() { u.pages.RemovePage("ua-path"); u.App.SetFocus(u.inspector.tree) }
	form.AddButton("浏览", func() {
		r := copyRequest(u.lastRequest)
		r.Action = "browse-path"
		r.Params["browse_path"] = form.GetFormItem(0).(*tview.InputField).GetText()
		delete(r.Params, "node_ids")
		close()
		u.start(r)
	}).AddButton("取消", close).SetCancelFunc(close)
	u.pages.AddPage("ua-path", form, true, true)
	u.App.SetFocus(form)
}
