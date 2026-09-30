package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"go.yaml.in/yaml/v3"
	"io"
	"os"
	"os/signal"

	"github.com/Live-yum/iotools/internal/engine"
	"github.com/Live-yum/iotools/internal/sample"
	"github.com/Live-yum/iotools/internal/tui"
)

var version = "dev"

func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "iotools:", e)
		os.Exit(1)
	}
}
func run(args []string) error {
	flags := flag.NewFlagSet("iotools", flag.ContinueOnError)
	importFormat := flags.String("import", "", "导入 slumber/v3/rest/openapi/insomnia 到新集合，禁止覆盖")
	input := flags.String("input", "", "导入源文件路径")
	path := flags.String("file", "iotools.yaml", "请求集合 YAML 文件路径")
	profile := flags.String("profile", "", "环境配置名称")
	curlRequest := flags.String("curl", "", "生成指定HTTP请求的POSIX curl命令，不执行命令，默认不触发依赖请求")
	executeTriggers := flags.Bool("execute-triggers", false, "生成curl时明确允许依赖请求；依赖修改仍需独立授权")
	request := flags.String("run", "", "执行指定请求 ID，输出 JSON 行")
	serveModbus := flags.String("serve-modbus", "", "以指定请求启动仅回环地址的 Modbus HTTP API")
	listen := flags.String("listen", "127.0.0.1:8082", "本机 API 监听地址，禁止公网绑定")
	allow := flags.Bool("allow-writes", false, "明确允许本次命令行修改操作")
	historySQL := flags.String("history-query", "", "执行只读 SQLite 查询，需要 --history-db，不连接服务器")
	historyDB := flags.String("history-db", "", "明确启用 HTTP SQLite 历史文件（可能保存响应中的敏感数据）")
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
				ctx := engine.WithHTTPWorkflowOptions(context.Background(), engine.HTTPWorkflowOptions{HistoryPath: *historyDB, AllowChainWrites: *allowChains})
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
		u.HTTPHistoryPath = *historyDB
		return u.Run()
	}
	for _, r := range c.Requests {
		if r.ID == *request {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			enc := json.NewEncoder(os.Stdout)
			ctx = engine.WithHTTPWorkflowOptions(ctx, engine.HTTPWorkflowOptions{HistoryPath: *historyDB, AllowChainWrites: *allowChains})
			return engine.RunCollection(ctx, c, r, *profile, *allow && !*readonly, func(e engine.Event) { _ = enc.Encode(e) })
		}
	}
	return fmt.Errorf("找不到请求 %q", *request)
}
