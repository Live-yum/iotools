//go:build cgo && !android

// This entrypoint is built as c-shared on desktop and c-archive on iOS.
// Android deliberately retains its independent JNI/USB bridge.
package main

/*
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import "unsafe"

func initReply(reply **C.uint8_t, n *C.size_t) bool {
	if reply != nil {
		*reply = nil
	}
	if n != nil {
		*n = 0
	}
	return reply != nil && n != nil
}

func writeReply(output string, reply **C.uint8_t, n *C.size_t) {
	if len(output) > maxNativeReply {
		output = nativeError("result exceeds 16 MiB; narrow the query")
	}
	data := C.malloc(C.size_t(len(output)))
	copy(unsafe.Slice((*byte)(data), len(output)), output)
	*reply = (*C.uint8_t)(data)
	*n = C.size_t(len(output))
}

func readInput(data *C.uint8_t, n C.size_t, limit uint64) (string, bool) {
	if data == nil || n == 0 || uint64(n) > limit {
		return "", false
	}
	return C.GoStringN((*C.char)(unsafe.Pointer(data)), C.int(n)), true
}

//export IotoolsNativeABIVersion
func IotoolsNativeABIVersion() C.uint32_t { return 1 }

//export IotoolsNativeOpen
func IotoolsNativeOpen(path *C.uint8_t, pathN C.size_t, root *C.uint8_t, rootN C.size_t, flags C.uint32_t, reply **C.uint8_t, replyN *C.size_t) C.uint64_t {
	if !initReply(reply, replyN) {
		return 0
	}
	p, pathOK := readInput(path, pathN, maxNativePath)
	r, rootOK := readInput(root, rootN, maxNativePath)
	if !pathOK || !rootOK {
		writeReply(nativeError("configuration path and app-private root must be 1 byte to 16 KiB"), reply, replyN)
		return 0
	}
	id, output := nativeSessions.open(p, r, uint32(flags))
	writeReply(output, reply, replyN)
	return C.uint64_t(id)
}

//export IotoolsNativeCommand
func IotoolsNativeCommand(id C.uint64_t, data *C.uint8_t, n C.size_t, reply **C.uint8_t, replyN *C.size_t) C.int32_t {
	if !initReply(reply, replyN) {
		return nativeInvalidArgument
	}
	input, ok := readInput(data, n, maxNativeCommand)
	if !ok {
		writeReply(nativeError("command must be 1 byte to 8 MiB"), reply, replyN)
		return nativeInvalidArgument
	}
	status, output := nativeSessions.command(uint64(id), input)
	writeReply(output, reply, replyN)
	return C.int32_t(status)
}

//export IotoolsNativeLifecycle
func IotoolsNativeLifecycle(id C.uint64_t, action C.int32_t) C.int32_t {
	return C.int32_t(nativeSessions.lifecycle(uint64(id), int32(action)))
}

//export IotoolsNativeFree
func IotoolsNativeFree(reply *C.uint8_t) { C.free(unsafe.Pointer(reply)) }

func main() {}
