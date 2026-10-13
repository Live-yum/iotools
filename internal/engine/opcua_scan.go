package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/ua"
)

// A scan shares the caller's authenticated session. It never discovers other
// endpoints or follows ExpandedNodeIDs naming another server. Namespace URIs
// are resolved only against the connected server's NamespaceArray.
type opcuaScanner interface {
	opcuaBrowser
	opcuaReader
}

type uaScanNode struct {
	id                      *ua.NodeID
	parent, display, browse string
	class                   ua.NodeClass
	depth                   int
}

func (n uaScanNode) data() map[string]any {
	return map[string]any{"node_id": n.id.String(), "parent_id": n.parent, "display_name": n.display, "browse_name": n.browse, "node_class": strings.TrimPrefix(n.class.String(), "NodeClass"), "depth": n.depth}
}

func opcuaScan(ctx context.Context, c opcuaScanner, r config.Request, emit Emit) (retErr error) {
	// Zero disables the optional depth limit. The iterative traversal remains
	// bounded by node/reference limits, deduplication and the caller's context.
	maxDepth, maxNodes := r.Int("max_depth", 0), r.Int("max_nodes", 10000)
	maxRefs := r.Int("max_references", 1000)
	if maxDepth < 0 || maxDepth > 256 || maxNodes < 1 || maxNodes > 100000 || maxRefs < 1 || maxRefs > 100000 {
		return fmt.Errorf("scan requires max_depth 0..256 (0 means unlimited depth), max_nodes 1..100000 and max_references 1..100000")
	}
	root, err := ua.ParseNodeID(r.String("node_id", "i=85"))
	if err != nil {
		return err
	}
	count, variables, failures, duplicates, external := 0, 0, 0, 0, 0
	reasons := []string{}
	defer func() {
		if retErr != nil && failures == 0 {
			failures = 1
		}
		if retErr != nil && len(reasons) == 0 {
			reasons = append(reasons, retErr.Error())
		}
		send(emit, "scan-summary", map[string]any{"nodes": count, "variables": variables, "complete": retErr == nil, "reason": strings.Join(reasons, "; "), "errors": failures, "duplicate_references": duplicates, "skipped_external": external, "cancelled": errors.Is(retErr, context.Canceled) || errors.Is(retErr, context.DeadlineExceeded)})
	}()
	recordError := func(node string, err error) {
		failures++
		// Bound retained diagnostics on very large or inaccessible address spaces.
		if len(reasons) < 10 {
			reasons = append(reasons, fmt.Sprintf("%s: %v", node, err))
		}
		send(emit, "scan-error", map[string]any{"node_id": node, "error": err.Error()})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	namespaces, fingerprint, err := opcuaScanNamespaces(ctx, c)
	if err != nil {
		recordError(root.String(), err)
		return err
	}
	send(emit, "scan-namespace", map[string]any{"fingerprint": fingerprint})
	first := uaScanNode{id: root}
	attributes := []*ua.ReadValueID{{NodeID: root, AttributeID: ua.AttributeIDNodeClass}, {NodeID: root, AttributeID: ua.AttributeIDDisplayName}, {NodeID: root, AttributeID: ua.AttributeIDBrowseName}}
	results, err := opcuaScanMetadata(ctx, c, attributes)
	if err != nil {
		recordError(root.String(), err)
		return err
	}
	for i, v := range results {
		if v == nil || v.Value == nil || v.Status != ua.StatusOK {
			return fmt.Errorf("scan root %s has unavailable %s", root, attributes[i].AttributeID)
		}
		switch i {
		case 0:
			switch class := v.Value.Value().(type) {
			case int32:
				first.class = ua.NodeClass(class)
			case uint32:
				first.class = ua.NodeClass(class)
			case ua.NodeClass:
				first.class = class
			default:
				return fmt.Errorf("invalid root NodeClass")
			}
		case 1:
			if text, ok := v.Value.Value().(*ua.LocalizedText); ok && text != nil {
				first.display = text.Text
			}
		case 2:
			if name, ok := v.Value.Value().(*ua.QualifiedName); ok && name != nil {
				first.browse = name.Name
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	queue := []uaScanNode{first}
	seen := map[string]bool{root.String(): true}
	emitNode := func(n uaScanNode) {
		count++
		if n.class == ua.NodeClassVariable {
			variables++
		}
		send(emit, "scan-node", n.data())
	}
	emitNode(first)
	depthTruncated := false
	for index := 0; index < len(queue); index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		parent := queue[index]
		err := opcuaScanReferences(ctx, c, parent.id, maxRefs, func(ref *ua.ReferenceDescription) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if ref == nil || ref.NodeID == nil || ref.NodeID.NodeID == nil {
				return fmt.Errorf("malformed hierarchical reference")
			}
			if !ref.IsForward {
				return nil
			}
			if ref.NodeID.ServerIndex != 0 {
				external++
				return nil
			}
			nodeID := ua.NewNodeIDFromExpandedNodeID(ref.NodeID)
			if ref.NodeID.NamespaceURI != "" {
				namespace, ok := namespaces[ref.NodeID.NamespaceURI]
				if !ok {
					recordError(parent.id.String(), fmt.Errorf("unresolved local namespace URI %q", ref.NodeID.NamespaceURI))
					return nil
				}
				switch nodeID.Type() {
				case ua.NodeIDTypeTwoByte, ua.NodeIDTypeFourByte, ua.NodeIDTypeNumeric:
					nodeID = ua.NewNumericNodeID(namespace, nodeID.IntID())
				default:
					if err := nodeID.SetNamespace(namespace); err != nil {
						return err
					}
				}
			}
			key := nodeID.String()
			if seen[key] {
				duplicates++
				return nil
			}
			if maxDepth > 0 && parent.depth >= maxDepth {
				depthTruncated = true
				return nil
			}
			if count >= maxNodes {
				return fmt.Errorf("scan reached max_nodes=%d", maxNodes)
			}
			seen[key] = true
			child := uaScanNode{id: nodeID, parent: parent.id.String(), class: ref.NodeClass, depth: parent.depth + 1}
			if ref.DisplayName != nil {
				child.display = ref.DisplayName.Text
			}
			if ref.BrowseName != nil {
				child.browse = ref.BrowseName.Name
			}
			emitNode(child)
			queue = append(queue, child)
			return nil
		})
		if err != nil {
			recordError(parent.id.String(), err)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if count >= maxNodes {
				return err
			}
		}
	}
	if depthTruncated {
		recordError(root.String(), fmt.Errorf("scan reached max_depth=%d", maxDepth))
	}
	if failures > 0 {
		return fmt.Errorf("OPC UA scan incomplete: %s", strings.Join(reasons, "; "))
	}
	return nil
}

// Pagination owns at most one continuation point at a time and releases it on
// every early exit, using a fresh bounded context even when the scan is cancelled.
func opcuaScanReferences(ctx context.Context, c opcuaBrowser, node *ua.NodeID, maxRefs int, visit func(*ua.ReferenceDescription) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	response, err := c.Browse(ctx, &ua.BrowseRequest{View: &ua.ViewDescription{}, RequestedMaxReferencesPerNode: 100, NodesToBrowse: []*ua.BrowseDescription{{NodeID: node, BrowseDirection: ua.BrowseDirectionForward, ReferenceTypeID: ua.NewNumericNodeID(0, id.HierarchicalReferences), IncludeSubtypes: true, ResultMask: uint32(ua.BrowseResultMaskAll)}}})
	if err != nil {
		return err
	}
	if response == nil {
		return fmt.Errorf("empty scan browse response")
	}
	results := response.Results
	var continuation []byte
	var unexpectedPoints [][]byte
	defer func() {
		points := unexpectedPoints
		if len(continuation) > 0 {
			points = append(points, continuation)
		}
		if len(points) == 0 {
			return
		}
		unique := make([][]byte, 0, len(points))
		seen := map[string]bool{}
		for _, point := range points {
			if len(point) > 0 && !seen[string(point)] {
				seen[string(point)] = true
				unique = append(unique, point)
			}
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = c.BrowseNext(cleanup, &ua.BrowseNextRequest{ReleaseContinuationPoints: true, ContinuationPoints: unique})
	}()
	references, pages := 0, 0
	emptyPoints := map[string]bool{}
	for {
		if len(results) != 1 || results[0] == nil {
			for _, result := range results {
				if result != nil {
					unexpectedPoints = append(unexpectedPoints, result.ContinuationPoint)
				}
			}
			return fmt.Errorf("malformed scan browse response")
		}
		result := results[0]
		continuation = result.ContinuationPoint
		if result.StatusCode != ua.StatusOK {
			return result.StatusCode
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		pages++
		if pages > maxRefs+1 {
			return fmt.Errorf("scan pagination exceeded bounded page count")
		}
		for _, ref := range result.References {
			if references >= maxRefs {
				return fmt.Errorf("scan reached max_references=%d", maxRefs)
			}
			references++
			if err := visit(ref); err != nil {
				return err
			}
		}
		if len(continuation) == 0 {
			return nil
		}
		if len(result.References) == 0 {
			key := string(continuation)
			if emptyPoints[key] {
				return fmt.Errorf("scan browse-next repeated an empty page")
			}
			emptyPoints[key] = true
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		next, err := c.BrowseNext(ctx, &ua.BrowseNextRequest{ContinuationPoints: [][]byte{continuation}})
		if err != nil {
			return err
		}
		if next == nil {
			return fmt.Errorf("empty scan browse-next response")
		}
		results = next.Results
	}
}

func opcuaScanNamespaces(ctx context.Context, c opcuaReader) (map[string]uint16, string, error) {
	res, err := c.Read(ctx, &ua.ReadRequest{TimestampsToReturn: ua.TimestampsToReturnNeither, NodesToRead: []*ua.ReadValueID{{NodeID: ua.NewNumericNodeID(0, id.Server_NamespaceArray), AttributeID: ua.AttributeIDValue}}})
	if err != nil {
		return nil, "", err
	}
	if res == nil || len(res.Results) != 1 || res.Results[0] == nil || res.Results[0].Value == nil || res.Results[0].Status != ua.StatusOK {
		return nil, "", fmt.Errorf("cannot read server NamespaceArray")
	}
	names, ok := res.Results[0].Value.Value().([]string)
	if !ok || len(names) == 0 || len(names) > 65536 {
		return nil, "", fmt.Errorf("invalid server NamespaceArray")
	}
	namespaces := make(map[string]uint16, len(names))
	for index, name := range names {
		if name == "" {
			return nil, "", fmt.Errorf("invalid server NamespaceArray: empty namespace URI")
		}
		if _, duplicate := namespaces[name]; duplicate {
			return nil, "", fmt.Errorf("invalid server NamespaceArray: duplicate namespace URI")
		}
		namespaces[name] = uint16(index)
	}
	encoded, err := json.Marshal(names)
	if err != nil {
		return nil, "", err
	}
	fingerprint := sha256.Sum256(encoded)
	return namespaces, hex.EncodeToString(fingerprint[:]), nil
}

// Servers may impose very small Read operation limits, even for the three root
// metadata attributes. Split only a rejected request; never omit an attribute.
func opcuaScanMetadata(ctx context.Context, c opcuaReader, attributes []*ua.ReadValueID) ([]*ua.DataValue, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	res, err := c.Read(ctx, &ua.ReadRequest{TimestampsToReturn: ua.TimestampsToReturnNeither, NodesToRead: attributes})
	if errors.Is(err, ua.StatusBadTooManyOperations) && len(attributes) > 1 {
		middle := len(attributes) / 2
		first, err := opcuaScanMetadata(ctx, c, attributes[:middle])
		if err != nil {
			return nil, err
		}
		rest, err := opcuaScanMetadata(ctx, c, attributes[middle:])
		if err != nil {
			return nil, err
		}
		return append(first, rest...), nil
	}
	if err != nil {
		return nil, err
	}
	if res == nil || len(res.Results) != len(attributes) {
		return nil, fmt.Errorf("incomplete scan root metadata")
	}
	return res.Results, nil
}

// Namespace indices are session-local identities. A selection made during a
// scan must fail closed if a subsequent session sees a reordered namespace table.
func opcuaVerifyNamespace(ctx context.Context, c opcuaReader, expected string) error {
	_, actual, err := opcuaScanNamespaces(ctx, c)
	if err != nil {
		return fmt.Errorf("cannot verify OPC UA namespace identity; rescan required: %w", err)
	}
	if !strings.EqualFold(expected, actual) {
		return fmt.Errorf("OPC UA NamespaceArray changed since scan; rescan required before reading or subscribing")
	}
	return nil
}
