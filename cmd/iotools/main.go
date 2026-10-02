package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go.yaml.in/yaml/v3"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/Live-yum/iotools/internal/sample"
	"github.com/Live-yum/iotools/internal/tui"
	_ "golang.org/x/crypto/x509roots/fallback" // embedded public roots when the OS has no verifier/root store
)

var version = "dev"

func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "iotools:", e)
		var status cliExitError
		if errors.As(e, &status) {
			os.Exit(status.code)
		}
		os.Exit(1)
	}
}
func run(args []string) error {
	flags := flag.NewFlagSet("iotools", flag.ContinueOnError)
	var fields, headers, queries, forms listFlag
	var bodyOverride, bearerOverride, basicOverride, urlOverride optionalFlag
	flags.Var(&urlOverride, "url", "临时HTTP目标URL模板，不替换原query")
	flags.Var(&fields, "set", "临时环境覆盖 name=value，可重复，不保存")
	flags.Var(&headers, "header", "临时HTTP头 name=value，无等号删除，可重复")
	flags.Var(&queries, "query", "临时query name=value，可重复同名")
	flags.Var(&forms, "form", "临时表单字段 name=value，无等号删除")
	flags.Var(&bodyOverride, "body", "临时正文，JSON先解析；不修改集合")
	flags.Var(&bearerOverride, "bearer", "临时Bearer模板，建议环境引用")
	flags.Var(&basicOverride, "basic", "临时Basic username:password模板，建议环境引用")
	importFormat := flags.String("import", "", "导入 slumber/v3/rest/openapi/insomnia 到新集合，禁止覆盖")
	input := flags.String("input", "", "导入源文件路径")
	path := flags.String("file", "iotools.yaml", "请求集合 YAML 文件路径")
	profile := flags.String("profile", "", "环境配置名称")
	bodyOnly := flags.Bool("response-body", false, "仅输出HTTP原始正文，不输出事件JSON")
	transformed := flags.Bool("transformed", false, "仅输出HTTP派生正文；转换失败返回3")
	output := flags.String("output", "", "HTTP正文保存到新文件，禁止覆盖；原始响应使用流式下载")
	verbose := flags.Bool("verbose", false, "正文输出时将HTTP状态/响应头写到stderr")
	exitStatus := flags.Bool("exit-status", false, "HTTP>=400时退出码2")
	dryRun := flags.Bool("dry-run", false, "仅生成所选请求curl预览，禁止依赖联网，需要--run")
	curlRequest := flags.String("curl", "", "生成指定HTTP请求的POSIX curl命令，不执行命令，默认不触发依赖请求")
	executeTriggers := flags.Bool("execute-triggers", false, "生成curl时明确允许依赖请求；依赖修改仍需独立授权")
	request := flags.String("run", "", "执行指定请求 ID，输出 JSON 行")
	serveModbus := flags.String("serve-modbus", "", "以指定请求启动仅回环地址的 Modbus HTTP API")
	listen := flags.String("listen", "127.0.0.1:8082", "本机 API 监听地址，禁止公网绑定")
	allow := flags.Bool("allow-writes", false, "明确允许本次命令行修改操作")
	historyList := flags.Bool("history-list", false, "列出当前集合的HTTP历史")
	historyGet := flags.Int64("history-get", 0, "查看当前集合指定HTTP历史ID")
	historyDelete := flags.String("history-delete", "", "永久删除当前集合的明确ID列表（逗号分隔）")
	allowHistoryDelete := flags.Bool("allow-history-delete", false, "明确允许本次不可恢复的本机历史删除")
	historySQL := flags.String("history-query", "", "执行只读 SQLite 查询，需要 --history-db，不连接服务器")
	historyScript := flags.String("history-script", "", "内置SQL脚本/.tables/.schema；默认只读，不联网")
	historyPreview := flags.Bool("history-preview", false, "只生成SQL写入快照令牌，不执行")
	historyApply := flags.String("history-apply", "", "执行与此预览令牌完全匹配的SQL写入")
	historyBackup := flags.String("history-backup", "", "写SQL前的新备份文件，禁止覆盖")
	historyCollections := flags.Bool("history-collections", false, "列出所选数据库全部历史集合")
	historyCollectionAction := flags.String("history-collection-action", "", "delete/migrate，默认预览不执行")
	historySource := flags.String("history-source", "", "明确来源集合标识")
	historyTarget := flags.String("history-target", "", "明确迁移目标集合标识")
	historyDB := flags.String("history-db", "", "明确启用 HTTP SQLite 历史文件（可能保存响应中的敏感数据）")
	allowInsecureTLS := flags.Bool("allow-insecure-tls", false, "本次明确允许配置列出的精确主机忽略TLS证书；存在中间人风险")
	allowChains := flags.Bool("allow-chain-writes", false, "明确允许请求链修改操作，必须同时指定 --allow-writes")
	readonly := flags.Bool("read-only", false, "只读模式，禁止所有修改操作")
	init := flags.Bool("init", false, "创建仅访问本机的示例配置（不覆盖已有文件）")
	validate := flags.Bool("validate", false, "校验配置，不连接服务器")
	showVersion := flags.Bool("version", false, "显示版本")
	if e := flags.Parse(args); e != nil {
		if e == flag.ErrHelp {
			return nil
		}
		return e
	}
	if *dryRun {
		if *request == "" || *curlRequest != "" || *executeTriggers {
			return fmt.Errorf("--dry-run需要--run，不能同时指定--curl/--execute-triggers")
		}
		*curlRequest = *request
		*request = ""
	}
	if (*bodyOnly || *transformed || *output != "" || *verbose || *exitStatus) && *request == "" {
		return fmt.Errorf("HTTP输出选项需要--run，不能用于dry-run/curl")
	}
	if (*bodyOnly || *transformed || *output != "" || *verbose || *exitStatus) && *curlRequest != "" {
		return fmt.Errorf("HTTP输出选项不能用于curl/dry-run")
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("无法识别的参数：%v", flags.Args())
	}
	if *importFormat != "" {
		if *input == "" {
			return fmt.Errorf("导入需要 --input 源文件")
		}
		f, err := os.Open(*input)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
		f.Close()
		if err != nil {
			return err
		}
		collection, err := engine.ImportCollection(data, *importFormat)
		if *importFormat == "slumber" || *importFormat == "v4" || *importFormat == "v5" {
			collection, _, err = engine.LoadCollection(*input)
		}
		if err != nil {
			return err
		}
		out, err := yaml.Marshal(collection)
		if err != nil {
			return err
		}
		dest, err := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = dest.Write(out)
		closeErr := dest.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Printf("已导入 %d 个请求到 %s（尚未连接任何服务器）\n", len(collection.Requests), *path)
		return nil
	}
	if *showVersion {
		fmt.Println("iotools", version)
		return nil
	}
	if *init {
		f, e := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(sample.Collection)
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		fmt.Println("已创建", *path, "· 启动：iotools --profile local")
		return nil
	}
	if *historyScript != "" || *historyCollections || *historyCollectionAction != "" || *historyPreview || *historyApply != "" {
		if *historyDB == "" {
			return fmt.Errorf("历史管理需要明确 --history-db")
		}
		if *historyCollections {
			if *historyScript != "" || *historyCollectionAction != "" || *historyApply != "" || *historyPreview {
				return fmt.Errorf("集合列表不可混合SQL写操作")
			}
			rows, err := engine.ListHTTPHistoryCollections(context.Background(), *historyDB)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(rows)
		}
		script := *historyScript
		if *historyCollectionAction != "" {
			if script != "" {
				return fmt.Errorf("集合操作不能同时指定SQL")
			}
			var err error
			script, err = engine.HTTPHistoryCollectionScript(*historyCollectionAction, *historySource, *historyTarget)
			if err != nil {
				return err
			}
		}
		if *historyApply != "" {
			if *readonly || !*allow || *historyPreview {
				return fmt.Errorf("执行历史SQL需要--allow-writes，不能只读或同时预览")
			}
			result, err := engine.ExecuteHTTPHistoryScript(context.Background(), *historyDB, script, *historyApply, *historyBackup, true)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(result)
		}
		if *historyPreview || *historyCollectionAction != "" {
			preview, err := engine.PreviewHTTPHistoryScript(context.Background(), *historyDB, script)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(preview)
		}
		result, err := engine.QueryHTTPHistoryScript(context.Background(), *historyDB, script)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if *historySQL != "" {
		if *historyDB == "" {
			return fmt.Errorf("--history-query 需要 --history-db")
		}
		rows, err := engine.QueryHTTPHistory(context.Background(), *historyDB, *historySQL)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(rows)
	}
	c, _, e := engine.LoadCollection(*path)
	if e != nil {
		return fmt.Errorf("%w（可用 --init 创建示例配置）", e)
	}
	if *profile == "" {
		*profile = c.DefaultProfile
	}
	if *allowChains && (!*allow || *readonly) {
		return fmt.Errorf("--allow-chain-writes 必须配合 --allow-writes，且不能在只读模式使用")
	}
	if *historyList || *historyGet > 0 || *historyDelete != "" {
		if *historyDB == "" {
			return fmt.Errorf("历史操作需要明确 --history-db")
		}
		if *historyDelete != "" {
			if *readonly {
				return fmt.Errorf("只读模式禁止删除历史")
			}
			ids := []int64{}
			for _, text := range strings.Split(*historyDelete, ",") {
				id, e := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
				if e != nil {
					return e
				}
				ids = append(ids, id)
			}
			count, e := engine.DeleteHTTPHistory(context.Background(), *historyDB, c.SourcePath, ids, *allowHistoryDelete)
			if e != nil {
				return e
			}
			fmt.Printf("已永久删除 %d 条历史\n", count)
			return nil
		}
		if *historyGet > 0 {
			entry, e := engine.GetHTTPHistory(context.Background(), *historyDB, c.SourcePath, *historyGet)
			if e != nil {
				return e
			}
			return json.NewEncoder(os.Stdout).Encode(entry)
		}
		rows, e := engine.ListHTTPHistory(context.Background(), *historyDB, c.SourcePath, "")
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(rows)
	}
	if len(fields)+len(headers)+len(queries)+len(forms) > 0 || bodyOverride.set || bearerOverride.set || basicOverride.set || urlOverride.set {
		id := *request
		if *curlRequest != "" {
			id = *curlRequest
		}
		if id == "" {
			return fmt.Errorf("临时覆盖需要 --run 或 --curl 请求ID")
		}
		found := false
		for i, r := range c.Requests {
			if r.ID == id {
				overrides := makeOverrides(fields, headers, queries, forms, bodyOverride, bearerOverride, basicOverride)
				overrides.URL = urlOverride.pointer()
				updated, recipe, selected, e := engine.ApplyHTTPOverrides(c, r, *profile, overrides)
				if e != nil {
					return e
				}
				updated.Requests = append([]config.Request(nil), c.Requests...)
				updated.Requests[i] = recipe
				c = updated
				*profile = selected
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("找不到请求 %q", id)
		}
	}
	if *validate {
		fmt.Printf("配置校验通过：%d 个请求，%d 个环境\n", len(c.Requests), len(c.Profiles))
		return nil
	}
	if *serveModbus != "" {
		if *request != "" {
			return fmt.Errorf("--serve-modbus 与 --run 不能同时使用")
		}
		for _, r := range c.Requests {
			if r.ID == *serveModbus {
				r, e = c.Resolve(r, *profile)
				if e != nil {
					return e
				}
				var scope *engine.ModbusWriteScope
				if *allow && !*readonly {
					for _, key := range []string{"unit", "address", "count"} {
						if _, ok := r.Params[key]; !ok {
							return fmt.Errorf("API 写入授权必须明确填写 %s", key)
						}
					}
					kind := ""
					switch r.Action {
					case "write-register", "write-registers":
						kind = "holding"
					case "write-coil", "write-coils":
						kind = "coil"
					default:
						return fmt.Errorf("API 写入授权必须选择写入请求，不能从读取请求扩大权限")
					}
					scope = &engine.ModbusWriteScope{Unit: r.Int("unit", 0), Address: r.Int("address", -1), Count: r.Int("count", 0), Type: kind}
				}
				ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
				defer stop()
				fmt.Fprintln(os.Stderr, "启动本机 Modbus API：", *listen, "；写入授权：", scope != nil, "；Ctrl-C 停止")
				return engine.ServeModbusAPI(ctx, *listen, r, scope)
			}
		}
		return fmt.Errorf("找不到请求 %q", *serveModbus)
	}
	if *curlRequest != "" {
		for _, r := range c.Requests {
			if r.ID == *curlRequest {
				ctx := engine.WithHTTPWorkflowOptions(context.Background(), engine.HTTPWorkflowOptions{HistoryPath: *historyDB, AllowChainWrites: *allowChains, AllowInsecureTLS: *allowInsecureTLS})
				command, err := engine.GenerateCurl(ctx, c, r, *profile, *allow && !*readonly, *executeTriggers)
				if err != nil {
					return err
				}
				fmt.Println(command)
				return nil
			}
		}
		return fmt.Errorf("找不到请求 %q", *curlRequest)
	}
	if *request == "" {
		u, err := tui.New(*path, *profile, *readonly)
		if err != nil {
			return err
		}
		u.BuildVersion = version
		u.HTTPHistoryPath = *historyDB
		return u.Run()
	}
	for _, r := range c.Requests {
		if r.ID == *request {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			enc := json.NewEncoder(os.Stdout)
			ctx = engine.WithHTTPWorkflowOptions(ctx, engine.HTTPWorkflowOptions{HistoryPath: *historyDB, AllowChainWrites: *allowChains, AllowInsecureTLS: *allowInsecureTLS})
			if *bodyOnly || *transformed || *output != "" || *verbose || *exitStatus {
				if r.Protocol != "http" {
					return fmt.Errorf("HTTP输出选项仅用于HTTP请求")
				}
				if _, exists := r.Params["response_file"]; exists && (*bodyOnly || *transformed) {
					return fmt.Errorf("正文输出模式不能与配置response_file同时使用")
				}
				if *bodyOnly && *transformed {
					return fmt.Errorf("--response-body与--transformed不能同时使用")
				}
				if *output != "" {
					absolute, err := filepath.Abs(*output)
					if err != nil {
						return err
					}
					*output = absolute
					if _, err := os.Lstat(*output); err == nil {
						return fmt.Errorf("输出文件已存在，拒绝覆盖")
					} else if !os.IsNotExist(err) {
						return err
					}
					if !*transformed {
						params := map[string]any{}
						for k, v := range r.Params {
							params[k] = v
						}
						params["response_file"] = *output
						r.Params = params
					}
				}
				display := httpDisplay{transformed: *transformed, verbose: *verbose, output: *output, stderr: os.Stderr}
				err := engine.RunCollection(ctx, c, r, *profile, *allow && !*readonly, display.event)
				return display.finish(os.Stdout, err, *exitStatus)
			}
			return engine.RunCollection(ctx, c, r, *profile, *allow && !*readonly, func(e engine.Event) { _ = enc.Encode(e) })
		}
	}
	return fmt.Errorf("找不到请求 %q", *request)
}
