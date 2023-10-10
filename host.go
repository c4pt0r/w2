package w2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

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

type HostFunc func(payload []byte) (ret []byte, err error)

type Host struct {
	r                      wazero.Runtime
	loadedMods             map[string]api.Module
	runtimePublicHostFuncs map[string]HostFunc
}

func _logString(ctx context.Context, m api.Module, offset, byteCount uint32) {
	buf, ok := m.Memory().Read(offset, byteCount)
	if !ok {
		slog.Error("Memory.Read(%d, %d) out of range", offset, byteCount)
		return
	}
	fmt.Println(string(buf))
}

func _callHost(ctx context.Context, m api.Module, offset, byteCount uint32) uint64 {
	buf, ok := m.Memory().Read(offset, byteCount)
	if !ok {
		slog.Error("Memory.Read(%d, %d) out of range", offset, byteCount)
		return 0
	}

	// Call the host function.
	host := extractHost(ctx)
	if host == nil {
		slog.Error("host not found in context")
		return 0
	}

	// TODO: handle function calls
	ret := []byte("hello from host, you're calling:" + string(buf))

	// Allocate memory for the return value. Wasm component should free it.
	ptr, err := m.ExportedFunction("malloc").Call(context.Background(), uint64(len(ret)))
	if err != nil {
		slog.Error("malloc failed: %v", err)
		return 0
	}
	if !m.Memory().Write(uint32(ptr[0]), []byte(ret)) {
		slog.Error("Memory.Write(%d, %d) out of range of memory size %d", ptr[0], len(ret), m.Memory().Size())
		return 0
	}
	retPtr := uint32(ptr[0])
	retSize := uint32(len(ret))
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
	h.r = r
	h.loadedMods = make(map[string]api.Module)
	return nil
}

func (h *Host) LoadMod(ctx context.Context, modWasmCode []byte) error {
	if h.r == nil {
		return errors.New("host not init")
	}
	mod, err := h.r.Instantiate(ctx, modWasmCode)
	if err != nil {
		return err
	}
	h.loadedMods[mod.Name()] = mod
	return nil
}

func (h *Host) HostContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, CtxKey_Host, h)
}

func (h *Host) Call(ctx context.Context, modName string, funcName string, payload []byte) (ret []byte, err error) {
	mod, ok := h.loadedMods[modName]
	if !ok {
		return nil, errors.New("module not found: " + modName)
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
		slog.Error("Memory.Write(%d, %d) out of range of memory size %d",
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
				slog.Error("free failed: %v", err)
			}
		}()
	}

	// The pointer is a linear memory offset, which is where we write the name.
	if bytes, ok := mod.Memory().Read(retPtr, retSize); !ok {
		slog.Error("Memory.Read(%d, %d) out of range of memory size %d",
			retPtr, retSize, mod.Memory().Size())
		return nil, errors.New("memory out of range")
	} else {
		return bytes, nil
	}
}
