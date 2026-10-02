package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Live-yum/iotools/internal/webhost"
)

var version = "web-dev"

func main() {
	var root string
	var port int
	flag.StringVar(&root, "data", "", "应用私有数据目录（默认用户配置目录/iotools-web）")
	flag.IntVar(&port, "port", 0, "本机监听端口，0 自动选择")
	flag.Parse()
	if port < 0 || port > 65535 {
		fmt.Fprintln(os.Stderr, "端口必须为 0–65535")
		os.Exit(2)
	}
	if root == "" {
		base, e := os.UserConfigDir()
		if e != nil {
			fmt.Fprintln(os.Stderr, "无法读取用户配置目录")
			os.Exit(1)
		}
		root = filepath.Join(base, "iotools-web")
	}
	absolute, e := filepath.Abs(root)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	listener, e := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	server, e := webhost.New(listener, webhost.Options{Root: absolute, Version: version, Assets: webAssets()})
	if e != nil {
		listener.Close()
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	defer server.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("iotools Web：%s\n仅监听本机。请在浏览器打开上述地址；Ctrl+C 关闭并取消全部活动操作。\n", server.URL())
	if e = server.Serve(ctx, listener); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
