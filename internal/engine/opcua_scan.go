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
	key                     string
	parent, display, browse string
	class                   ua.NodeClass
	depth                   int
}

func (n uaScanNode) data() map[string]any {
	return map[string]any{"node_id": n.key, "parent_id": n.parent, "display_name": n.display, "browse_name": n.browse, "node_class": strings.TrimPrefix(n.class.String(), "NodeClass"), "depth": n.depth}
}

func opcuaScan(ctx context.Context, c opcuaScanner, r config.Request, emit Emit) (retErr error) {
	// Zero disables the optional depth limit. The iterative traversal remains
	// bounded by node/reference limits, deduplication and the caller's context.
	maxDepth, maxNodes := r.Int("max_depth", 0), r.Int("max_nodes", 10000)
	maxRefs := r.Int("max_references", 1000)
	if maxDepth < 0 || maxDepth > 256 || maxNodes < 1 || maxNodes > 100000 || maxRefs < 1 || maxRefs > 100000 {
		return fmt.Errorf("scan requires max_depth 0..256 (0 means unlimited depth), max_nodes 1..100000 and max_references 1..100000")
	}
	memory := &opcuaScanMemory{remaining: opcuaScanMemoryLimit}
	count, variables, failures, duplicates, external := 0, 0, 0, 0, 0
	reasons := []string{}
	defer func() {
		if retErr != nil && failures == 0 {
			failures = 1
		}
		// Fixed terminal diagnostics remain visible even after ten ordinary
		// errors or after the metadata allowance has been fully consumed.
		for _, terminal := range []error{errOPCUAScanMemoryLimit, errOPCUAScanReleaseLimit, errOPCUAScanReleaseFailed} {
			if errors.Is(retErr, terminal) && !strings.Contains(strings.Join(reasons, "; "), terminal.Error()) {
				reasons = append(reasons, terminal.Error())
			}
		}
		if retErr != nil && len(reasons) == 0 {
			reasons = append(reasons, retErr.Error())
		}
		send(emit, "scan-summary", map[string]any{"nodes": count, "variables": variables, "complete": retErr == nil, "reason": strings.Join(reasons, "; "), "errors": failures, "duplicate_references": duplicates, "skipped_external": external, "cancelled": errors.Is(retErr, context.Canceled) || errors.Is(retErr, context.DeadlineExceeded)})
	}()
	recordError := func(node string, err error) error {
		message := err.Error()
		trial := *memory
		if trial.bytes(len(node)) != nil || trial.bytes(len(message)) != nil {
			bounded := error(errOPCUAScanMemoryLimit)
			for _, terminal := range []error{context.Canceled, context.DeadlineExceeded, errOPCUAScanReleaseLimit, errOPCUAScanReleaseFailed} {
				if errors.Is(err, terminal) {
					bounded = errors.Join(bounded, terminal)
				}
			}
			err = bounded
			message = err.Error()
			node = ""
		} else {
			*memory = trial
			message = strings.Clone(message)
		}
		failures++
		// Bound retained diagnostics on very large or inaccessible address spaces.
		if len(reasons) < 10 {
			reasons = append(reasons, fmt.Sprintf("%s: %s", node, message))
		}
		send(emit, "scan-error", map[string]any{"node_id": node, "error": message})
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	rootText := r.String("node_id", "i=85")
	if err := memory.bytes(len(rootText)); err != nil {
		return err
	}
	root, err := ua.ParseNodeID(rootText)
	if err != nil {
		return err
	}
	root, rootKey, err := memory.ownNodeID(root)
	if err != nil {
		return err
	}
	namespaces, fingerprint, err := opcuaScanNamespacesBounded(ctx, c, memory)
	if err != nil {
		return recordError(rootKey, err)
	}
	send(emit, "scan-namespace", map[string]any{"fingerprint": fingerprint})
	first := uaScanNode{id: root, key: rootKey}
	attributes := []*ua.ReadValueID{{NodeID: root, AttributeID: ua.AttributeIDNodeClass}, {NodeID: root, AttributeID: ua.AttributeIDDisplayName}, {NodeID: root, AttributeID: ua.AttributeIDBrowseName}}
	results, err := opcuaScanMetadata(ctx, c, attributes)
	if err != nil {
		return recordError(rootKey, err)
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
	first, err = memory.ownNames(first)
	if err != nil {
		return recordError(rootKey, err)
	}
	// The frontier never owns display/browse metadata. Clear consumed entries
	// so previous IDs also stop pinning buffers while a deep scan progresses.
	type frontierNode struct {
		id    *ua.NodeID
		key   string
		depth int
	}
	queue := []frontierNode{{id: root, key: rootKey}}
	seen := map[string]bool{rootKey: true}
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
		queue[index] = frontierNode{}
		err := opcuaScanReferencesBounded(ctx, c, parent.id, maxRefs, memory, func(ref *ua.ReferenceDescription) error {
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
			nodeID, key, err := memory.ownNodeID(ref.NodeID.NodeID)
			if err != nil {
				return err
			}
			if ref.NodeID.NamespaceURI != "" {
				namespace, ok := namespaces[ref.NodeID.NamespaceURI]
				if !ok {
					if err := memory.bytes(len(ref.NodeID.NamespaceURI)); err != nil {
						return err
					}
					err := recordError(parent.key, fmt.Errorf("unresolved local namespace URI %q", ref.NodeID.NamespaceURI))
					if errors.Is(err, errOPCUAScanMemoryLimit) {
						return err
					}
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
				key = nodeID.String()
			}
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
			child := uaScanNode{id: nodeID, key: key, parent: parent.key, class: ref.NodeClass, depth: parent.depth + 1}
			if ref.DisplayName != nil {
				child.display = ref.DisplayName.Text
			}
			if ref.BrowseName != nil {
				child.browse = ref.BrowseName.Name
			}
			child, err = memory.ownNames(child)
			if err != nil {
				return err
			}
			seen[key] = true
			emitNode(child)
			queue = append(queue, frontierNode{id: nodeID, key: key, depth: child.depth})
			return nil
		})
		if err != nil {
			err = recordError(parent.key, err)
			if ctx.Err() != nil {
				return errors.Join(ctx.Err(), err)
			}
			if count >= maxNodes || errors.Is(err, errOPCUAScanMemoryLimit) || errors.Is(err, errOPCUAScanReleaseFailed) {
				return err
			}
		}
	}
	if depthTruncated {
		if err := recordError(rootKey, fmt.Errorf("scan reached max_depth=%d", maxDepth)); errors.Is(err, errOPCUAScanMemoryLimit) {
			return err
		}
	}
	if failures > 0 {
		return fmt.Errorf("OPC UA scan incomplete: %s", strings.Join(reasons, "; "))
	}
	return nil
}

// Pagination owns at most one continuation point at a time and attempts bounded
// release on early exits with a fresh context, including cancellation. Oversized
// tokens and unconfirmed releases remain explicit errors.
func opcuaScanReferences(ctx context.Context, c opcuaBrowser, node *ua.NodeID, maxRefs int, visit func(*ua.ReferenceDescription) error) error {
	return opcuaScanReferencesBounded(ctx, c, node, maxRefs, &opcuaScanMemory{remaining: opcuaScanMemoryLimit}, visit)
}

func opcuaScanReferencesBounded(ctx context.Context, c opcuaBrowser, node *ua.NodeID, maxRefs int, memory *opcuaScanMemory, visit func(*ua.ReferenceDescription) error) (retErr error) {
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
	var unexpectedResults []*ua.BrowseResult
	defer func() {
		// Malformed responses can contain many points. Release in constant-size
		// requests, without constructing another unbounded token/string list.
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		seen := map[[sha256.Size]byte]bool{}
		cleanupError := func(err error) {
			if !errors.Is(retErr, err) {
				retErr = errors.Join(retErr, err)
			}
		}
		release := func(point []byte) {
			if len(point) == 0 {
				return
			}
			if len(point) > opcuaScanCleanupTokenLimit {
				cleanupError(errOPCUAScanReleaseLimit)
				return
			}
			if cleanup.Err() != nil {
				cleanupError(errOPCUAScanReleaseFailed)
				return
			}
			key := sha256.Sum256(point)
			if seen[key] {
				return
			}
			if len(seen) < 1024 {
				seen[key] = true
			}
			released, err := c.BrowseNext(cleanup, &ua.BrowseNextRequest{ReleaseContinuationPoints: true, ContinuationPoints: [][]byte{point}})
			if err != nil || released == nil || (released.ResponseHeader != nil && released.ResponseHeader.ServiceResult != ua.StatusOK) || len(released.Results) > 1 {
				cleanupError(errOPCUAScanReleaseFailed)
				return
			}
			// Release responses may omit results. If present, the sole token's
			// status must be Good; nil Go error alone does not confirm release.
			if len(released.Results) == 1 && (released.Results[0] == nil || released.Results[0].StatusCode != ua.StatusOK) {
				cleanupError(errOPCUAScanReleaseFailed)
			}
		}
		for _, result := range unexpectedResults {
			if cleanup.Err() != nil {
				cleanupError(errOPCUAScanReleaseFailed)
				break
			}
			if result != nil {
				release(result.ContinuationPoint)
			}
		}
		release(continuation)
	}()
	references, pages := 0, 0
	emptyPoints := map[[sha256.Size]byte]bool{}
	for {
		if len(results) != 1 || results[0] == nil {
			unexpectedResults = results
			return fmt.Errorf("malformed scan browse response")
		}
		result := results[0]
		// Keep the current decoded token borrowed for cleanup even if admission
		// fails. Normal pagination retains only an admitted exact-length clone.
		continuation = result.ContinuationPoint
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(continuation) > opcuaScanCleanupTokenLimit {
			return errOPCUAScanReleaseLimit
		}
		if len(continuation) > 0 {
			owned, err := memory.ownPoint(continuation)
			if err != nil {
				return err
			}
			continuation = owned
		}
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
			key := sha256.Sum256(continuation)
			if emptyPoints[key] {
				return fmt.Errorf("scan browse-next repeated an empty page")
			}
			if err := memory.charge(128); err != nil {
				return err
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
	return opcuaScanNamespacesBounded(ctx, c, &opcuaScanMemory{remaining: opcuaScanMemoryLimit})
}

func opcuaScanNamespacesBounded(ctx context.Context, c opcuaReader, memory *opcuaScanMemory) (map[string]uint16, string, error) {
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
	trial := *memory
	if err := trial.charge(int64(len(names)) * 128); err != nil {
		return nil, "", err
	}
	for _, name := range names {
		if err := trial.bytes(len(name)); err != nil {
			return nil, "", err
		}
	}
	*memory = trial
	namespaces := make(map[string]uint16, len(names))
	for index, name := range names {
		if name == "" {
			return nil, "", fmt.Errorf("invalid server NamespaceArray: empty namespace URI")
		}
		if _, duplicate := namespaces[name]; duplicate {
			return nil, "", fmt.Errorf("invalid server NamespaceArray: duplicate namespace URI")
		}
		namespaces[strings.Clone(name)] = uint16(index)
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
