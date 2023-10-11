package w2

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/c4pt0r/log"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type CtxKey string

var (
	CtxKey_Host = CtxKey("host")
)

func extractHost(ctx context.Context) *Host {
	if v, ok := ctx.Value(CtxKey_Host).(*Host); ok {
		return v
	}
	return nil
}

type ParamType map[string]interface{}
type ResultType interface{}
type HostFunc func(param ParamType) (result ResultType, err error)

type Host struct {
	rt               wazero.Runtime
	loadedMods       map[string]api.Module
	builtinHostFuncs map[string]HostFunc
}

type CallReq struct {
	Method string    `json:"method"`
	Params ParamType `json:"params"`
}

type CallResp struct {
	Result ResultType `json:"result,omitempty"`
	Error  string     `json:"error,omitempty"`
}

func NewHost() *Host {
	h := new(Host)
	builtinHostFuncs := map[string]HostFunc{
		"list_modules": func(param ParamType) (ResultType, error) {
			mods := make([]string, 0, len(h.loadedMods))
			for k := range h.loadedMods {
				mods = append(mods, k)
			}
			return mods, nil
		},
		"list_builtins": func(param ParamType) (ResultType, error) {
			builtins := make([]string, 0, len(h.builtinHostFuncs))
			for k := range h.builtinHostFuncs {
				builtins = append(builtins, k)
			}
			return builtins, nil
		},
	}
	h.builtinHostFuncs = builtinHostFuncs
	h.loadedMods = make(map[string]api.Module)
	return h
}

func _logString(ctx context.Context, m api.Module, offset, byteCount uint32) {
	buf, ok := m.Memory().Read(offset, byteCount)
	if !ok {
		log.Errorf("Memory.Read(%d, %d) out of range", offset, byteCount)
		return
	}
	log.Infof("log from wasm: %s", string(buf))
}

func _callHost(ctx context.Context, m api.Module, offset, byteCount uint32) uint64 {
	buf, ok := m.Memory().Read(offset, byteCount)
	if !ok {
		log.Errorf("Memory.Read(%d, %d) out of range", offset, byteCount)
		return 0
	}

	// Call the host function.
	host := extractHost(ctx)
	if host == nil {
		log.Errorf("host not found in context")
		return 0
	}

	// delegate to builtin host functions
	var req CallReq
	var resp CallResp
	err := json.Unmarshal(buf, &req)
	if err != nil {
		log.Errorf("json.Unmarshal failed: %v", err)
		resp.Error = err.Error()
		// quick return here
		goto E
	}
	if f, ok := host.builtinHostFuncs[req.Method]; ok {
		resp.Result, err = f(req.Params)
		if err != nil {
			log.Errorf("builtin host function %s failed: %v", req.Method, err)
			resp.Error = err.Error()
		}
	} else {
		resp.Error = "host function not found"
	}

E:
	ret := toJSON(resp)
	// Allocate memory for the return value. Wasm component should free it.
	ptr, err := m.ExportedFunction("malloc").Call(context.Background(), uint64(len(ret)))
	if err != nil {
		log.Errorf("malloc failed: %v", err)
		return 0
	}
	if !m.Memory().Write(uint32(ptr[0]), []byte(ret)) {
		log.Errorf("Memory.Write(%d, %d) out of range of memory size %d", ptr[0], len(ret), m.Memory().Size())
		return 0
	}
	retPtr := uint32(ptr[0])
	retSize := uint32(len(ret))
	// hack from https://github.com/tetratelabs/wazero/blob/main/examples/allocation/tinygo/greet.go
	return uint64(retPtr)<<32 | uint64(retSize)
}

func (h *Host) Init() error {
	// Choose the context to use for function calls.
	ctx := context.WithValue(context.Background(), CtxKey_Host, h)
	// Create a new WebAssembly Runtime.
	r := wazero.NewRuntime(ctx)

	_, err := r.NewHostModuleBuilder("env").
		NewFunctionBuilder().WithFunc(_callHost).Export("call_host").
		NewFunctionBuilder().WithFunc(_logString).Export("log").
		Instantiate(ctx)
	if err != nil {
		r.Close(ctx)
		return err
	}
	// Note: testdata/greet.go doesn't use WASI, but TinyGo needs it to
	// implement functions such as panic.
	wasi_snapshot_preview1.MustInstantiate(ctx, r)
	h.rt = r
	h.loadedMods = make(map[string]api.Module)
	return nil
}

func (h *Host) LoadMod(ctx context.Context, modName string, modWasmCode []byte) error {
	if h.rt == nil {
		return errors.New("host not init")
	}
	mod, err := h.rt.Instantiate(ctx, modWasmCode)
	if err != nil {
		return err
	}
	h.loadedMods[modName] = mod
	log.Infof("loaded module: %s", modName)
	return nil
}

func (h *Host) Call(ctx context.Context, modName string, method string, params ParamType) (ret ResultType, err error) {
	ctx = context.WithValue(ctx, CtxKey_Host, h)
	mod, ok := h.loadedMods[modName]
	if !ok {
		return nil, errors.New("module not found: " + modName)
	}

	req := CallReq{
		Method: method,
		Params: params,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	doFunc := mod.ExportedFunction("do")
	// These are undocumented, but exported. See tinygo-org/tinygo#2788
	malloc := mod.ExportedFunction("malloc")
	free := mod.ExportedFunction("free")

	argSize := uint64(len(payload))
	mallocRet, err := malloc.Call(ctx, argSize)
	if err != nil {
		return nil, err
	}
	argPtr := uint32(mallocRet[0])

	if !mod.Memory().Write(uint32(argPtr), payload) {
		log.Errorf("Memory.Write(%d, %d) out of range of memory size %d",
			argPtr, argSize, mod.Memory().Size())
		return nil, errors.New("memory out of range")
	}

	ptrSize, err := doFunc.Call(ctx, uint64(argPtr), argSize)
	if err != nil {
		return nil, err
	}

	retPtr := uint32(ptrSize[0] >> 32)
	retSize := uint32(ptrSize[0])

	// This pointer is managed by TinyGo, but TinyGo is unaware of external usage.
	// So, we have to free it when finished
	if retPtr != 0 {
		defer func() {
			_, err := free.Call(ctx, uint64(retPtr))
			if err != nil {
				log.Errorf("free failed: %v", err)
			}
		}()
	}

	// The pointer is a linear memory offset, which is where we write the name.
	if bytes, ok := mod.Memory().Read(retPtr, retSize); !ok {
		log.Errorf("Memory.Read(%d, %d) out of range of memory size %d",
			retPtr, retSize, mod.Memory().Size())
		return nil, errors.New("memory out of range")
	} else {
		var ret CallResp
		err := json.Unmarshal(bytes, &ret)
		if err != nil {
			return nil, err
		}
		if ret.Error != "" {
			return nil, errors.New(ret.Error)
		}
		return ret.Result, nil
	}
}
