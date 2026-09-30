package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"
)

func opcuaAttribute(raw any) (ua.AttributeID, error) {
	if raw == nil {
		return ua.AttributeIDValue, nil
	}
	if text, ok := raw.(string); ok {
		for id := ua.AttributeIDNodeID; id <= ua.AttributeIDAccessLevelEx; id++ {
			if strings.EqualFold(strings.TrimPrefix(id.String(), "AttributeID"), text) {
				return id, nil
			}
		}
	}
	n, e := exactInt(raw)
	if e != nil || n < 1 || n > 27 {
		return 0, fmt.Errorf("attribute 要求有效属性名称或 1..27")
	}
	return ua.AttributeID(n), nil
}
func opcuaAttributes(r config.Request) ([]ua.AttributeID, error) {
	if r.Action != "attributes" {
		id, e := opcuaAttribute(r.Params["attribute"])
		return []ua.AttributeID{id}, e
	}
	raw, ok := r.Params["attributes"]
	if !ok {
		out := make([]ua.AttributeID, 0, 27)
		for id := ua.AttributeIDNodeID; id <= ua.AttributeIDAccessLevelEx; id++ {
			out = append(out, id)
		}
		return out, nil
	}
	values, ok := raw.([]any)
	if !ok || len(values) < 1 || len(values) > 27 {
		return nil, fmt.Errorf("attributes 必须是 1..27 个属性的列表")
	}
	out := []ua.AttributeID{}
	seen := map[ua.AttributeID]bool{}
	for _, v := range values {
		id, e := opcuaAttribute(v)
		if e != nil {
			return nil, e
		}
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out, nil
}
func opcuaReadAttributes(ctx context.Context, c *opcua.Client, r config.Request, emit Emit) error {
	nodes, e := opcuaNodes(r)
	if e != nil {
		return e
	}
	attrs, e := opcuaAttributes(r)
	if e != nil {
		return e
	}
	req := &ua.ReadRequest{TimestampsToReturn: ua.TimestampsToReturnBoth}
	for _, n := range nodes {
		for _, a := range attrs {
			req.NodesToRead = append(req.NodesToRead, &ua.ReadValueID{NodeID: n, AttributeID: a})
		}
	}
	res, e := c.Read(ctx, req)
	if e != nil {
		return e
	}
	if res == nil || len(res.Results) != len(req.NodesToRead) {
		return fmt.Errorf("incomplete OPC UA attribute read response")
	}
	good := 0
	failed := false
	for i, v := range res.Results {
		if v == nil {
			return fmt.Errorf("empty OPC UA attribute result")
		}
		n := req.NodesToRead[i]
		data := opcuaData(n.NodeID.String(), v)
		data["attribute"] = strings.TrimPrefix(n.AttributeID.String(), "AttributeID")
		data["attribute_id"] = uint32(n.AttributeID)
		if r.Action == "read" && n.AttributeID == ua.AttributeIDValue {
			send(emit, "value", data)
		} else {
			send(emit, "attribute", data)
		}
		if v.Status == ua.StatusOK {
			good++
		} else {
			failed = true
		}
	}
	if good == 0 || (r.Action == "read" && failed) {
		return fmt.Errorf("OPC UA 属性读取返回非 Good 状态，请检查逐属性结果")
	}
	return nil
}
func validateOPCUAOperation(r config.Request) error {
	switch r.Action {
	case "method-arguments":
		_, e := ua.ParseNodeID(r.String("method_id", r.String("node_id", "")))
		return e
	case "read", "attributes":
		if _, e := opcuaNodes(r); e != nil {
			return e
		}
		_, e := opcuaAttributes(r)
		return e
	case "write":
		if _, e := ua.ParseNodeID(r.String("node_id", "")); e != nil {
			return e
		}
		id, e := opcuaAttribute(r.Params["attribute"])
		if e != nil {
			return e
		}
		expected := map[ua.AttributeID]string{ua.AttributeIDDisplayName: "LocalizedText", ua.AttributeIDDescription: "LocalizedText", ua.AttributeIDBrowseName: "QualifiedName", ua.AttributeIDHistorizing: "Boolean", ua.AttributeIDExecutable: "Boolean", ua.AttributeIDUserExecutable: "Boolean", ua.AttributeIDIsAbstract: "Boolean", ua.AttributeIDSymmetric: "Boolean", ua.AttributeIDContainsNoLoops: "Boolean", ua.AttributeIDWriteMask: "UInt32", ua.AttributeIDUserWriteMask: "UInt32", ua.AttributeIDAccessLevelEx: "UInt32", ua.AttributeIDAccessLevel: "Byte", ua.AttributeIDUserAccessLevel: "Byte", ua.AttributeIDEventNotifier: "Byte", ua.AttributeIDMinimumSamplingInterval: "Double", ua.AttributeIDValueRank: "Int32"}
		if id != ua.AttributeIDValue {
			kind, ok := expected[id]
			if !ok {
				return fmt.Errorf("该属性不在允许写入的属性列表中")
			}
			if r.String("value_type", "") != kind {
				return fmt.Errorf("属性 %s 需要 value_type: %s", id, kind)
			}
		}
		_, e = opcuaVariant(r.String("value_type", ""), r.Params["value"])
		return e
	case "call":
		if _, e := ua.ParseNodeID(r.String("object_id", "")); e != nil {
			return e
		}
		if _, e := ua.ParseNodeID(r.String("method_id", "")); e != nil {
			return e
		}
		_, e := opcuaArguments(r.Params["arguments"])
		return e
	case "browse", "references":
		if _, e := ua.ParseNodeID(r.String("node_id", "i=85")); e != nil {
			return e
		}
		_, e := opcuaBrowseDirection(r)
		if e != nil {
			return e
		}
		if value := r.String("reference_type", ""); value != "" {
			_, e = ua.ParseNodeID(value)
		}
		return e
	}
	return nil
}
func opcuaBrowseDirection(r config.Request) (ua.BrowseDirection, error) {
	def := "forward"
	if r.Action == "references" {
		def = "both"
	}
	switch r.String("direction", def) {
	case "forward":
		return ua.BrowseDirectionForward, nil
	case "inverse":
		return ua.BrowseDirectionInverse, nil
	case "both":
		return ua.BrowseDirectionBoth, nil
	default:
		return 0, fmt.Errorf("direction 需要 forward/inverse/both")
	}
}
