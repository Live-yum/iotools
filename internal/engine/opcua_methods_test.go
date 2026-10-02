package engine

import (
	"context"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua/ua"
	"testing"
)

func TestMethodArgumentTypes(t *testing.T) {
	for _, rank := range []int32{-1, 1} {
		a, e := methodArgument(&ua.Argument{Name: "温度", DataType: ua.NewNumericNodeID(0, 6), ValueRank: rank})
		if e != nil || (rank == 1 && a.Type != "Int32[]") || (rank == -1 && a.Type != "Int32") {
			t.Fatalf("%+v %v", a, e)
		}
	}
	for _, a := range []*ua.Argument{nil, {DataType: ua.NewNumericNodeID(2, 6), ValueRank: -1}, {DataType: ua.NewNumericNodeID(0, 6), ValueRank: 2}} {
		if _, e := methodArgument(a); e == nil {
			t.Fatal("unsupported argument accepted")
		}
	}
}
func TestMethodDiscoveryDoesNotInvoke(t *testing.T) {
	endpoint, _, params := localUAServer(t, false)
	params["node_id"] = "i=85"
	found := false
	err := Run(context.Background(), config.Request{Protocol: "opcua", Action: "method-arguments", Endpoint: endpoint, Params: params}, false, func(e Event) {
		if e.Kind == "method-arguments" {
			found = true
			m := e.Data.(map[string]any)
			if len(m["inputs"].([]MethodArgument)) != 0 {
				t.Error("unexpected method inputs")
			}
		}
	})
	if err != nil || !found {
		t.Fatalf("%v %v", found, err)
	}
}
