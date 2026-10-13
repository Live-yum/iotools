package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/Live-yum/iotools/internal/sample"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"go.yaml.in/yaml/v3"
)

func TestSampleStartupDefaultAndExplicitProfilesWithoutExecution(t *testing.T) {
	c, err := config.Parse(sample.Collection)
	if err != nil {
		t.Fatal(err)
	}
	c.Profiles["alternate"] = map[string]string{}
	for key, value := range c.Profiles["local"] {
		c.Profiles["alternate"][key] = strings.ReplaceAll(value, "127.0.0.1", "127.0.0.2")
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "iotools.yaml")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ profile, want string }{{"", "local"}, {"local", "local"}, {"alternate", "alternate"}} {
		t.Run("profile="+tc.profile, func(t *testing.T) {
			u, err := New(p, tc.profile, true)
			if err != nil {
				t.Fatal(err)
			}
			if u.profile != tc.want {
				t.Fatalf("startup profile = %q, want %q", u.profile, tc.want)
			}
			s := tcell.NewSimulationScreen("UTF-8")
			u.App.SetScreen(s)
			s.SetSize(110, 38)
			defer s.Fini()
			for i, r := range c.Requests {
				u.list.SetCurrentItem(i)
				u.App.ForceDraw()
				resolved, err := u.collection.Resolve(r, u.profile)
				want := strings.ReplaceAll(r.Endpoint, "${"+r.Protocol+"}", c.Profiles[tc.want][r.Protocol])
				if err != nil || resolved.Endpoint != want {
					t.Errorf("%s endpoint = %q, want %q: %v", r.ID, resolved.Endpoint, want, err)
				}
			}
			if u.running || u.cancel != nil || u.lastRequest.ID != "" {
				t.Error("startup/selection started a request")
			}
			selectNoProfile(u)
			if u.profile != "" {
				t.Fatalf("no-profile choice selected %q", u.profile)
			}
			if _, err := engine.GenerateCurl(context.Background(), explicitProfileCollection(u.collection, u.profile), c.Requests[0], u.profile, false, false); err == nil || !strings.Contains(err.Error(), `unresolved variable "http"`) {
				t.Fatalf("no-profile curl error = %v", err)
			}
			if u.running || u.cancel != nil {
				t.Error("selecting no profile started a request")
			}
		})
	}
	if _, err := New(p, "missing", true); err == nil || !strings.Contains(err.Error(), `unknown profile "missing"`) {
		t.Fatalf("unknown profile error = %v", err)
	}
	after, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(after, data) {
		t.Fatalf("startup or profile selection changed file: %v", err)
	}
}

func TestStartupDoesNotGuessUndeclaredDefaultProfile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "custom.yaml")
	data := strings.Replace(string(sample.Collection), "default_profile: local\n", "", 1)
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	u, err := New(p, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if u.profile != "" {
		t.Fatalf("guessed undeclared profile %q", u.profile)
	}
	if _, err := engine.GenerateCurl(context.Background(), explicitProfileCollection(u.collection, u.profile), u.collection.Requests[0], u.profile, false, false); err == nil {
		t.Fatal("resolved variables without a selected profile")
	}
}

func TestNoProfileHTTPWorkflowDoesNotFallBackToDefault(t *testing.T) {
	for _, mode := range []string{"execute", "curl", "override", "override-field"} {
		t.Run(mode, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				_, _ = w.Write([]byte("ok"))
			}))
			t.Cleanup(server.Close)
			u, _ := newTestUI(t)
			u.collection.DefaultProfile = "local"
			u.collection.Profiles["local"]["http"] = server.URL
			u.collection.Profiles["local"]["default-only"] = "must-not-inherit"
			u.selected = 0
			startProfileTestUI(t, u)
			u.App.QueueUpdateDraw(func() {
				selectNoProfile(u)
				if u.profile != "" {
					t.Errorf("no-profile selection = %q", u.profile)
				}
				switch mode {
				case "execute":
					u.execute()
				case "curl":
					u.httpConsole()
					_, page := u.pages.GetFrontPage()
					form := page.(*tview.Form)
					form.GetFormItem(0).(*tview.DropDown).SetCurrentOption(2)
					profileClick(form.GetButton(0))
				default:
					u.httpOverrideForm()
					_, page := u.pages.GetFrontPage()
					form := page.(*tview.Form)
					wantProfile := ""
					if mode == "override-field" {
						encoded, _ := json.Marshal(map[string]any{"Fields": []string{"http=" + server.URL}})
						form.GetFormItem(1).(*tview.TextArea).SetText(string(encoded), false)
						wantProfile = "@temporary"
					}
					profileClick(form.GetButton(0))
					_, page = u.pages.GetFrontPage()
					preview := page.(*tview.Flex)
					text := preview.GetItem(0).(*tview.TextView).GetText(false)
					if !strings.Contains(text, `"profile": "`+wantProfile+`"`) || strings.Contains(text, "must-not-inherit") {
						t.Errorf("override inherited default environment: %s", text)
					}
					profileClick(preview.GetItem(1).(*tview.Form).GetButton(0))
				}
			})
			deadline := time.Now().Add(5 * time.Second)
			running := true
			for running && time.Now().Before(deadline) {
				u.App.QueueUpdateDraw(func() { running = u.running })
				if running {
					time.Sleep(time.Millisecond)
				}
			}
			if running {
				t.Fatal("HTTP workflow did not finish")
			}
			u.App.QueueUpdateDraw(func() {
				if mode == "override-field" {
					if hits.Load() != 1 || !strings.Contains(u.status.GetText(false), "执行完成") {
						t.Errorf("explicit temporary field should run once: hits=%d, %s", hits.Load(), u.status.GetText(false))
					}
				} else {
					if hits.Load() != 0 {
						t.Errorf("no-profile operation contacted default endpoint %d times", hits.Load())
					}
					if mode == "curl" {
						if !u.pages.HasPage("modal") || u.pages.HasPage("http-query-result") {
							t.Error("curl rendered using the default profile")
						}
					} else if !strings.Contains(u.status.GetText(false), `unresolved variable "http"`) {
						t.Errorf("unexpected execution status: %s", u.status.GetText(false))
					}
				}
				if u.profile != "" || u.collection.DefaultProfile != "local" || u.collection.Profiles["local"]["default-only"] != "must-not-inherit" {
					t.Error("operation changed selected profile or source collection")
				}
			})
		})
	}
}

func selectNoProfile(u *UI) {
	u.profiles()
	_, page := u.pages.GetFrontPage()
	list := page.(*tview.List)
	list.SetCurrentItem(0)
	list.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
}

func profileClick(button *tview.Button) {
	button.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
}

func startProfileTestUI(t *testing.T, u *UI) {
	t.Helper()
	ready := make(chan struct{})
	var once sync.Once
	u.App.SetAfterDrawFunc(func(tcell.Screen) { once.Do(func() { close(ready) }) })
	done := make(chan error, 1)
	go func() { done <- u.Run() }()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		u.App.Stop()
		t.Fatal("UI startup timed out")
	}
	t.Cleanup(func() {
		u.App.QueueUpdateDraw(u.quit)
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("UI shutdown timed out")
		}
	})
}
