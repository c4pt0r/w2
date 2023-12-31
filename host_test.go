package w2

import (
	"context"
	"fmt"
	"os"
	"testing"
)

func loadWasm(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func TestCallFunc(t *testing.T) {
	code, err := loadWasm("test_plugin/plugin.wasm")
	if err != nil {
		t.Fatal(err)
	}
	// init host
	host := NewHost()
	err = host.Init()
	if err != nil {
		t.Fatal(err)
	}

	// load wasm
	ctx := context.Background()
	err = host.LoadMod(ctx, "hello_mod", code)
	if err != nil {
		t.Fatal(err)
	}

	ret, err := host.Call(ctx, "hello_mod", "echo", ParamType{
		"msg": "hello world",
	})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(ret)

	/*
		ret, err = host.Call(ctx, "hello_mod", "curl", map[string]interface{}{
			"url": "google.com",
		})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println(ret)
	*/

	ret, err = host.Call(ctx, "hello_mod", "stat", nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(ret)
}
