package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/Live-yum/iotools/internal/config"
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
	path := flags.String("file", "iotools.yaml", "请求集合 YAML 文件路径")
	profile := flags.String("profile", "", "环境配置名称")
	request := flags.String("run", "", "执行指定请求 ID，输出 JSON 行")
	serveModbus := flags.String("serve-modbus", "", "以指定请求启动仅回环地址的 Modbus HTTP API")
	listen := flags.String("listen", "127.0.0.1:8082", "本机 API 监听地址，禁止公网绑定")
	allow := flags.Bool("allow-writes", false, "明确允许本次命令行修改操作")
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
	c, _, e := config.Load(*path)
	if e != nil {
		return fmt.Errorf("%w（可用 --init 创建示例配置）", e)
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
	if *request == "" {
		return tui.Run(*path, *profile, *readonly)
	}
	for _, r := range c.Requests {
		if r.ID == *request {
			r, e = c.Resolve(r, *profile)
			if e != nil {
				return e
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			enc := json.NewEncoder(os.Stdout)
			return engine.Run(ctx, r, *allow && !*readonly, func(e engine.Event) { _ = enc.Encode(e) })
		}
	}
	return fmt.Errorf("找不到请求 %q", *request)
}
