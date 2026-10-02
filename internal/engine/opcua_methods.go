package engine

import (
	"context"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"
)

// MethodArgument describes the server's declared argument without invoking it.
type MethodArgument struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	DataType    string `json:"data_type"`
	ValueRank   int32  `json:"value_rank"`
	Description string `json:"description"`
}

func methodArgument(a *ua.Argument) (MethodArgument, error) {
	if a == nil || a.DataType == nil {
		return MethodArgument{}, fmt.Errorf("方法参数缺少 DataType")
	}
	types := map[uint32]string{1: "Boolean", 2: "SByte", 3: "Byte", 4: "Int16", 5: "UInt16", 6: "Int32", 7: "UInt32", 8: "Int64", 9: "UInt64", 10: "Float", 11: "Double", 12: "String", 13: "DateTime", 14: "Guid", 17: "NodeId", 15: "ByteString", 20: "QualifiedName", 21: "LocalizedText"}
	kind := ""
	if a.DataType.Namespace() == 0 {
		kind = types[a.DataType.IntID()]
	}
	if kind == "" {
		return MethodArgument{}, fmt.Errorf("不支持方法参数 %s 的自定义类型 %s", a.Name, a.DataType)
	}
	if a.ValueRank == 1 {
		kind += "[]"
	} else if a.ValueRank != -1 {
		return MethodArgument{}, fmt.Errorf("方法参数 %s 仅支持标量或一维数组", a.Name)
	}
	d := ""
	if a.Description != nil {
		d = a.Description.Text
	}
	return MethodArgument{a.Name, kind, a.DataType.String(), a.ValueRank, d}, nil
}
func opcuaMethodArguments(ctx context.Context, c *opcua.Client, r config.Request, emit Emit) error {
	method := r.String("method_id", r.String("node_id", ""))
	if _, e := ua.ParseNodeID(method); e != nil {
		return e
	}
	params := map[string]any{"node_id": method, "reference_type": "i=46", "max_references": 256}
	properties := map[string]string{}
	e := opcuaBrowse(ctx, c, config.Request{Action: "browse", Params: params}, func(event Event) {
		ref, ok := event.Data.(*ua.ReferenceDescription)
		if ok && ref.BrowseName != nil && ref.NodeID != nil && ref.NodeID.NodeID != nil && ref.NodeID.ServerIndex == 0 && ref.NodeID.NamespaceURI == "" {
			if ref.BrowseName.NamespaceIndex == 0 && (ref.BrowseName.Name == "InputArguments" || ref.BrowseName.Name == "OutputArguments") {
				properties[ref.BrowseName.Name] = ref.NodeID.NodeID.String()
			}
		}
	})
	if e != nil {
		return e
	}
	result := map[string]any{"method_id": method, "inputs": []MethodArgument{}, "outputs": []MethodArgument{}}
	for _, name := range []string{"InputArguments", "OutputArguments"} {
		node, ok := properties[name]
		if !ok {
			continue
		}
		n, _ := ua.ParseNodeID(node)
		response, err := c.Read(ctx, &ua.ReadRequest{NodesToRead: []*ua.ReadValueID{{NodeID: n, AttributeID: ua.AttributeIDValue}}})
		if err != nil {
			return err
		}
		if response == nil || len(response.Results) != 1 || response.Results[0] == nil || response.Results[0].Status != ua.StatusOK || response.Results[0].Value == nil {
			return fmt.Errorf("方法参数属性读取失败：%s", name)
		}
		values, ok := response.Results[0].Value.Value().([]*ua.ExtensionObject)
		if !ok {
			return fmt.Errorf("方法参数属性不是 Argument[]：%s", name)
		}
		if len(values) > 64 {
			return fmt.Errorf("方法参数超过64个")
		}
		args := []MethodArgument{}
		for _, v := range values {
			if v == nil {
				return fmt.Errorf("空方法参数")
			}
			a, ok := v.Value.(*ua.Argument)
			if !ok {
				return fmt.Errorf("无效方法参数扩展对象")
			}
			arg, err := methodArgument(a)
			if err != nil {
				return err
			}
			args = append(args, arg)
		}
		if name == "InputArguments" {
			result["inputs"] = args
		} else {
			result["outputs"] = args
		}
	}
	send(emit, "method-arguments", result)
	return nil
}
