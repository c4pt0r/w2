package main

// #include <stdlib.h>
import "C"
import (
	"encoding/json"
	"errors"
	"unsafe"
)

type PluginFunc func(param map[string]interface{}) (result interface{}, err error)

type CallReq struct {
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params"`
}
type CallResp struct {
	Result interface{} `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
}

var RegisteredFunctions = map[string]PluginFunc{
	"echo": func(param map[string]interface{}) (result interface{}, err error) {
		var msg string
		for k, v := range param {
			// output log in the host's console
			log(k + ": " + toJSON(v))
			// generate the result
			msg += k + "=>" + toJSON(v) + "\n"
		}
		return msg, nil
	},

	"curl": func(param map[string]interface{}) (result interface{}, err error) {
		url, ok := param["url"].(string)
		if !ok {
			return nil, errors.New("url is not a string")
		}
		// TODO: callHost warpper
		return "TODO", nil
	},
}

/* W2 helper functions */
//go:wasmimport env call_host
func _callHost(paramPtr, paramSize uint32) uint64
func callHost(buf string) string {
	ptr, size := gostr_to_ptr(buf)
	ret := _callHost(ptr, size)
	retPtr, retSize := unpack_uint64_to_uint32(ret)
	if retPtr != 0 {
		defer free_ptr(retPtr)
	}
	return ptr_to_gostr(retPtr, retSize)
}

//go:wasmimport env log
func _log(ptr, size uint32)

func log(s string) {
	ptr, size := gostr_to_ptr(s)
	_log(ptr, size)
}

//export do
func _do(ptr, size uint32) (ptrSize uint64) {
	// param's memory is managed by the host
	param := ptr_to_gostr(ptr, size)
	ret := do(param)
	// need to dup the string because the host will free the pointer
	ptr, size = dup_gostr(ret)
	return (uint64(ptr) << uint64(32)) | uint64(size)
}

func toJSON(v interface{}) string {
	buf, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(buf)
}

/* main functions */
func do(payload string) string {
	// dispatch to the right function
	// payload is a JSON string
	// {
	//   "method": "echo",
	//   "params": ...
	// }

	// return the result as a JSON string
	// {
	//   "result": ... // or "error": ...
	// }
	var req CallReq
	err := json.Unmarshal([]byte(payload), &req)
	if err != nil {
		return toJSON(CallResp{Error: err.Error()})
	}

	if fn, ok := RegisteredFunctions[req.Method]; ok {
		result, err := fn(req.Params)
		if err != nil {
			return toJSON(CallResp{Error: err.Error()})
		}
		return toJSON(CallResp{Result: result})
	} else {
		return toJSON(CallResp{Error: "method not found"})
	}
}

// ptr_to_gostr returns a string from WebAssembly compatible numeric types
// representing its pointer and length.
func ptr_to_gostr(ptr uint32, size uint32) string {
	return unsafe.String((*byte)(unsafe.Pointer(uintptr(ptr))), size)
}

// gostr_to_ptr returns a pointer and size pair for the given string in a way
// compatible with WebAssembly numeric types.
// The returned pointer aliases the string hence the string must be kept alive
// until ptr is no longer needed.
func gostr_to_ptr(s string) (uint32, uint32) {
	ptr := unsafe.Pointer(unsafe.StringData(s))
	return uint32(uintptr(ptr)), uint32(len(s))
}

// dup_gostr returns a pointer and size pair for the given string in a way
// The pointer is not automatically managed by TinyGo hence it must be freed by the host.
func dup_gostr(s string) (uint32, uint32) {
	size := C.ulong(len(s))
	ptr := unsafe.Pointer(C.malloc(size))
	copy(unsafe.Slice((*byte)(ptr), size), s)
	return uint32(uintptr(ptr)), uint32(size)
}

// free_ptr frees the given pointer.
func free_ptr(ptr uint32) {
	if ptr == 0 {
		return
	}
	C.free(unsafe.Pointer(uintptr(ptr)))
}

func unpack_uint64_to_uint32(v uint64) (ptr uint32, size uint32) {
	return uint32(v >> 32), uint32(v)
}

func pack_uint32_to_uint64(ptr, size uint32) uint64 {
	return (uint64(ptr) << 32) | uint64(size)
}

func uint64_to_gostr(v uint64) (s string, ptr uint32) {
	ptr, size := unpack_uint64_to_uint32(v)
	return ptr_to_gostr(ptr, size), ptr
}

func main() {}
