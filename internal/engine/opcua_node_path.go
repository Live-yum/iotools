package engine

import (
	"context"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"
	"strings"
)

func opcuaNodeBrowsePath(ctx context.Context, c *opcua.Client, r config.Request, emit Emit) error {
	current, e := ua.ParseNodeID(r.String("node_id", "i=85"))
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	segments := []string{}
	for depth := 0; depth < 64; depth++ {
		if current.Equal(ua.NewNumericNodeID(0, 84)) {
			for l, h := 0, len(segments)-1; l < h; l, h = l+1, h-1 {
				segments[l], segments[h] = segments[h], segments[l]
			}
			send(emit, "browse-path", map[string]any{"node_id": r.String("node_id", "i=85"), "path": "/" + strings.Join(segments, "/")})
			return nil
		}
		if seen[current.String()] {
			return fmt.Errorf("浏览路径父引用形成循环")
		}
		seen[current.String()] = true
		name, e := c.Node(current).BrowseName(ctx)
		if e != nil {
			return e
		}
		if name == nil {
			return fmt.Errorf("节点缺少BrowseName")
		}
		segments = append(segments, escapeUABrowseName(name))
		parents := []*ua.NodeID{}
		e = opcuaBrowse(ctx, c, config.Request{Action: "references", Params: map[string]any{"node_id": current.String(), "direction": "inverse", "reference_type": "i=33", "include_subtypes": true, "max_references": 1000}}, func(event Event) {
			ref, ok := event.Data.(*ua.ReferenceDescription)
			if ok && ref.NodeID != nil && ref.NodeID.NodeID != nil && ref.NodeID.ServerIndex == 0 && ref.NodeID.NamespaceURI == "" {
				parents = append(parents, ref.NodeID.NodeID)
			}
		})
		if e != nil {
			return e
		}
		if len(parents) != 1 {
			return fmt.Errorf("节点存在%d个层级父引用，不能生成唯一浏览路径", len(parents))
		}
		current = parents[0]
	}
	return fmt.Errorf("浏览路径超过64层")
}
