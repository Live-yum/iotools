package engine

import (
	"context"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua/ua"
	"strconv"
	"strings"
)

func parseUABrowsePath(text string) ([]*ua.QualifiedName, error) {
	if len(text) > 8192 {
		return nil, fmt.Errorf("浏览路径超过8192字节")
	}
	segments := []string{}
	var part strings.Builder
	escaped := false
	for _, r := range text {
		if escaped {
			part.WriteRune('&')
			part.WriteRune(r)
			escaped = false
			continue
		}
		if r == '&' {
			escaped = true
			continue
		}
		if r == '/' {
			if part.Len() > 0 {
				segments = append(segments, part.String())
				part.Reset()
			}
			continue
		}
		part.WriteRune(r)
	}
	if escaped {
		return nil, fmt.Errorf("浏览路径末尾缺少转义字符")
	}
	if part.Len() > 0 {
		segments = append(segments, part.String())
	}
	if len(segments) > 0 && strings.EqualFold(segments[0], "Root") {
		segments = segments[1:]
	}
	if len(segments) > 64 {
		return nil, fmt.Errorf("浏览路径最多64层")
	}
	result := []*ua.QualifiedName{}
	for _, raw := range segments {
		ns := uint64(0)
		body := raw
		colon := -1
		escaped = false
		for i, r := range raw {
			if escaped {
				escaped = false
				continue
			}
			if r == '&' {
				escaped = true
				continue
			}
			if r == ':' {
				colon = i
				break
			}
		}
		if colon >= 0 {
			prefix := strings.TrimPrefix(raw[:colon], "ns=")
			if n, e := strconv.ParseUint(prefix, 10, 16); e == nil {
				ns = n
				body = raw[colon+1:]
			} else if strings.HasPrefix(raw, "ns=") {
				return nil, fmt.Errorf("无效命名空间编号")
			}
		}
		var name strings.Builder
		escaped = false
		for _, r := range body {
			if !escaped && r == '&' {
				escaped = true
				continue
			}
			name.WriteRune(r)
			escaped = false
		}
		if name.Len() == 0 {
			return nil, fmt.Errorf("浏览路径含空名称")
		}
		result = append(result, &ua.QualifiedName{NamespaceIndex: uint16(ns), Name: name.String()})
	}
	return result, nil
}
func resolveUABrowsePath(ctx context.Context, c opcuaBrowser, text string) (*ua.NodeID, error) {
	segments, e := parseUABrowsePath(text)
	if e != nil {
		return nil, e
	}
	current := ua.NewNumericNodeID(0, 84)
	for _, target := range segments {
		matches := []*ua.NodeID{}
		r := config.Request{Action: "browse", Params: map[string]any{"node_id": current.String(), "reference_type": "i=33", "include_subtypes": true, "max_references": 10000}}
		e = opcuaBrowse(ctx, c, r, func(event Event) {
			ref, ok := event.Data.(*ua.ReferenceDescription)
			if !ok || ref.BrowseName == nil || ref.NodeID == nil || ref.NodeID.NodeID == nil {
				return
			}
			if ref.BrowseName.NamespaceIndex == target.NamespaceIndex && ref.BrowseName.Name == target.Name && ref.NodeID.ServerIndex == 0 && ref.NodeID.NamespaceURI == "" {
				matches = append(matches, ref.NodeID.NodeID)
			}
		})
		if e != nil {
			return nil, e
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("路径节点 %d:%s 在 %s 下匹配%d项，需要唯一匹配", target.NamespaceIndex, target.Name, current, len(matches))
		}
		current = matches[0]
	}
	return current, nil
}
