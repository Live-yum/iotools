//go:build android

package main

/*
#include <stdlib.h>
int IotoolsUSBExchange(char*,void*,int,int,int,int,char*,int,void*,int);
void IotoolsUSBClose(void);
*/
import "C"
import (
	"context"
	"fmt"
	"github.com/Live-yum/iotools/internal/engine"
	"time"
	"unsafe"
)

type androidUSB struct{}

func (androidUSB) Close() error { C.IotoolsUSBClose(); return nil }
func (androidUSB) Exchange(ctx context.Context, endpoint string, request []byte, settings engine.ModbusRTUSettings, timeout time.Duration) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(request) < 4 || len(request) > 256 {
		return nil, fmt.Errorf("USB 请求长度无效")
	}
	name := C.CString(endpoint)
	defer C.free(unsafe.Pointer(name))
	parity := C.CString(settings.Parity)
	defer C.free(unsafe.Pointer(parity))
	ms := (timeout + time.Millisecond - 1) / time.Millisecond
	if ms < 1 || ms > 5000 {
		return nil, fmt.Errorf("USB 超时时间无效")
	}
	response := make([]byte, 256)
	n := int(C.IotoolsUSBExchange(name, unsafe.Pointer(&request[0]), C.int(len(request)), C.int(settings.Baud), C.int(settings.DataBits), C.int(settings.StopBits), parity, C.int(ms), unsafe.Pointer(&response[0]), 256))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n < 5 || n > 256 {
		return nil, fmt.Errorf("USB 串口通信失败：检查设备授权、端口和串口设置（%d）", n)
	}
	return response[:n], nil
}
